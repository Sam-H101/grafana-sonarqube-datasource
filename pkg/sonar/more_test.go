package sonar_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar/sonartest"
)

func TestProjectsTagged(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	if _, _, err := c.ProjectsTagged(ctx, 10, []string{"backend", "c++"}); err != nil {
		t.Fatal(err)
	}
	// Unquoted values: SonarQube Cloud matches nothing for quoted ones.
	if got := f.Calls("api/components/search_projects")[0].Query.Get("filter"); got != "tags IN (backend,c++)" {
		t.Errorf("filter = %q", got)
	}
	f.Reset()
	if _, _, err := c.Projects(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if f.Calls("api/components/search_projects")[0].Query.Has("filter") {
		t.Error("a filter was sent without tags")
	}
}

// The hotspot search names its project parameter "projectKey" on SonarQube
// Cloud and Server 9.9, and "project" on Server 26.11.
func TestHotspotsOnEveryFlavour(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		f := sonartest.New(t)
		f.Cloud = cloud
		c := client(t, f, sonar.Config{Token: "t", Cloud: cloud})
		hotspots, total, err := c.Hotspots(ctx, "p", sonar.Ref{PullRequest: "7"}, "TO_REVIEW", 50)
		if err != nil || total != 3 || len(hotspots) != 3 {
			t.Fatalf("cloud=%v: %d hotspots, total %d, %v", cloud, len(hotspots), total, err)
		}
		q := f.Calls("api/hotspots/search")[0].Query
		if q.Get("status") != "TO_REVIEW" || q.Get("pullRequest") != "7" || q.Get("ps") != "50" || q.Has("branch") {
			t.Errorf("cloud=%v: query = %v", cloud, q)
		}
	}

	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	hotspots, _, err := c.Hotspots(ctx, "demo:legacy-billing", sonar.Ref{}, "", 2)
	if err != nil || len(hotspots) != 2 {
		t.Fatalf("limit 2: %d hotspots, %v", len(hotspots), err)
	}
	h := hotspots[0]
	if h.File() != "src/client.py" || h.Line == nil || *h.Line != 13 || h.Status != "REVIEWED" || h.Resolution != "SAFE" || h.RuleKey != "python:S1313" {
		t.Errorf("first hotspot = %+v", h)
	}
	if _, ok := sonar.ParseTime(h.CreationDate); !ok {
		t.Errorf("creation date %q", h.CreationDate)
	}
}

func TestPullRequests(t *testing.T) {
	f := sonartest.New(t)
	f.Cloud = true
	c := client(t, f, sonar.Config{Organization: "sonarsource", Cloud: true})
	prs, err := c.PullRequests(ctx, "p")
	if err != nil || len(prs) != 2 {
		t.Fatalf("%d pull requests, %v", len(prs), err)
	}
	pr := prs[1]
	if pr.Key != "767" || pr.Branch != "feature/mcp-workspace-env" || pr.Target != "master" || pr.Status.QualityGateStatus != "ERROR" || pr.Status.CodeSmells == nil || *pr.Status.CodeSmells != 1 {
		t.Errorf("pull request = %+v", pr)
	}
	if got := f.Calls("api/project_pull_requests/list")[0].Query.Get("project"); got != "p" {
		t.Errorf("project = %q", got)
	}

	// Community Build does not have the endpoint at all.
	community := sonartest.New(t)
	_, err = client(t, community, sonar.Config{Token: "t"}).PullRequests(ctx, "p")
	if !errors.Is(err, sonar.ErrNoPullRequests) {
		t.Errorf("community: %v", err)
	}
	// An unknown project is still SonarQube's own error.
	f.Handlers["api/project_pull_requests/list"] = func(w http.ResponseWriter, _ *http.Request) {
		sonartest.Error(w, http.StatusNotFound, "Component key 'nope' not found")
	}
	_, err = c.PullRequests(ctx, "nope")
	var apiErr *sonar.APIError
	if !errors.As(err, &apiErr) || !apiErr.NotFound() || errors.Is(err, sonar.ErrNoPullRequests) {
		t.Errorf("unknown project: %v", err)
	}
}

func TestPullRequestIsSentInsteadOfABranch(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	ref := sonar.Ref{PullRequest: "42"}
	if _, err := c.MeasureHistory(ctx, "p", []string{"coverage"}, ref, time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	// With a pull request, measures are read per project, as with a branch.
	if _, err := c.Measures(ctx, []string{"a", "b"}, []string{"coverage"}, ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Issues(ctx, sonar.IssueFilter{Projects: []string{"p"}, Ref: ref}, 5); err != nil {
		t.Fatal(err)
	}
	if n := len(f.Calls("api/measures/component")); n != 2 {
		t.Errorf("%d per-project measure requests, want 2", n)
	}
	for _, path := range []string{"api/measures/search_history", "api/measures/component", "api/issues/search"} {
		for _, call := range f.Calls(path) {
			if call.Query.Get("pullRequest") != "42" || call.Query.Has("branch") {
				t.Errorf("%s: %v", path, call.Query)
			}
		}
	}
}
