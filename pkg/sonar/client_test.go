package sonar_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar/sonartest"
)

func client(t *testing.T, f *sonartest.Fake, cfg sonar.Config) *sonar.Client {
	t.Helper()
	cfg.BaseURL = f.URL
	c, err := sonar.NewClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var ctx = context.Background()

func TestTokenIsSentAsBasicUserName(t *testing.T) {
	f := sonartest.New(t)
	f.Token = "squ_secret"
	c := client(t, f, sonar.Config{Token: "squ_secret"})
	if _, _, err := c.Projects(ctx, 10); err != nil {
		t.Fatal(err)
	}
	got := f.Calls("api/components/search_projects")[0].Header.Get("Authorization")
	// base64("squ_secret:")
	if got != "Basic c3F1X3NlY3JldDo=" {
		t.Errorf("Authorization = %q", got)
	}
	if !c.HasToken() {
		t.Error("HasToken is false")
	}
}

func TestStatusAndTokenValidation(t *testing.T) {
	f := sonartest.New(t)
	f.Token = "good"
	s, err := client(t, f, sonar.Config{Token: "good"}).Status(ctx)
	if err != nil || s.Status != "UP" || s.Version != "26.11.0.134175" {
		t.Fatalf("status = %+v, %v", s, err)
	}
	for token, want := range map[string]bool{"good": true, "bad": false, "": false} {
		valid, err := client(t, f, sonar.Config{Token: token}).ValidToken(ctx)
		if err != nil || valid != want {
			t.Errorf("ValidToken(%q) = %v, %v; want %v", token, valid, err, want)
		}
	}
}

func TestErrors(t *testing.T) {
	f := sonartest.New(t)
	f.Token = "good"

	// Wrong or missing token.
	for _, token := range []string{"bad", ""} {
		_, _, err := client(t, f, sonar.Config{Token: token}).Projects(ctx, 10)
		if !errors.Is(err, sonar.ErrUnauthorized) {
			t.Errorf("token %q: %v, want ErrUnauthorized", token, err)
		}
	}

	c := client(t, f, sonar.Config{Token: "good"})

	// An API error carries SonarQube's message.
	_, err := c.MeasureHistory(ctx, "nope", []string{"coverage"}, sonar.Ref{}, time.Time{}, time.Time{})
	var apiErr *sonar.APIError
	if !errors.As(err, &apiErr) || !apiErr.NotFound() || apiErr.Message != "Component key 'nope' not found" {
		t.Errorf("unknown project: %v", err)
	}

	f.Handlers["api/metrics/search"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
	if _, err := c.Metrics(ctx); !errors.Is(err, sonar.ErrForbidden) {
		t.Errorf("403: %v, want ErrForbidden", err)
	}

	// A web page instead of the API: a wrong URL or a login page.
	f.Handlers["api/metrics/search"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
	}
	if _, err := c.Metrics(ctx); !errors.Is(err, sonar.ErrNotSonarQube) {
		t.Errorf("HTML answer: %v, want ErrNotSonarQube", err)
	}
	f.Handlers["api/metrics/search"] = http.NotFound
	if _, err := c.Metrics(ctx); !errors.Is(err, sonar.ErrNotSonarQube) {
		t.Errorf("plain 404: %v, want ErrNotSonarQube", err)
	}

	// A long, multi-line API message is flattened and bounded.
	f.Handlers["api/metrics/search"] = func(w http.ResponseWriter, _ *http.Request) {
		sonartest.Error(w, http.StatusBadRequest, "line one\nline two "+strings.Repeat("x", 2000))
	}
	_, err = c.Metrics(ctx)
	if !errors.As(err, &apiErr) || strings.Contains(apiErr.Message, "\n") || len(apiErr.Message) > 520 {
		t.Errorf("long message not cleaned: %d bytes", len(fmt.Sprint(err)))
	}

	// A response over the size cap is refused, not buffered.
	f.Handlers["api/metrics/search"] = func(w http.ResponseWriter, _ *http.Request) {
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < 34; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}
	if _, err := c.Metrics(ctx); !errors.Is(err, sonar.ErrResponseTooLarge) {
		t.Errorf("oversized answer: %v, want ErrResponseTooLarge", err)
	}
}

func TestProjects(t *testing.T) {
	f := sonartest.New(t)
	projects, total, err := client(t, f, sonar.Config{Token: "t"}).Projects(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(projects) != 3 || projects[0].Key != "demo:legacy-billing" || projects[0].Name != "Legacy Billing" {
		t.Errorf("projects = %+v (total %d)", projects, total)
	}
	if got := f.Calls("api/components/search_projects")[0].Query; got.Has("organization") {
		t.Errorf("organization sent to a server: %v", got)
	}

	// A limit below the page size truncates but still reports the total.
	projects, total, _ = client(t, f, sonar.Config{Token: "t"}).Projects(ctx, 1)
	if len(projects) != 1 || total != 3 {
		t.Errorf("limit 1: %d projects, total %d", len(projects), total)
	}
}

func TestCloudSendsOrganization(t *testing.T) {
	f := sonartest.New(t)
	f.Cloud = true
	c := client(t, f, sonar.Config{Organization: "sonarsource", Cloud: true})
	projects, total, err := c.Projects(ctx, 10)
	if err != nil || total != 83 || len(projects) != 5 {
		t.Fatalf("%d projects, total %d, err %v", len(projects), total, err)
	}
	if got := f.Calls("api/components/search_projects")[0].Query.Get("organization"); got != "sonarsource" {
		t.Errorf("organization = %q", got)
	}

	// The wrong organization is an API error with Cloud's message.
	_, _, err = client(t, f, sonar.Config{Organization: "typo", Cloud: true}).Projects(ctx, 10)
	var apiErr *sonar.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "No organization for key 'typo'") {
		t.Errorf("wrong organization: %v", err)
	}

	// Issue searches are scoped to the organization too, and use the
	// parameter name Cloud accepts.
	if _, _, err := c.Issues(ctx, sonar.IssueFilter{Projects: []string{"a", "b"}}, 10); err != nil {
		t.Fatal(err)
	}
	q := f.Calls("api/issues/search")[0].Query
	if q.Get("organization") != "sonarsource" || q.Get("componentKeys") != "a,b" || q.Has("components") {
		t.Errorf("issue search query = %v", q)
	}
}

func TestMeasureHistory(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 21, 12, 30, 0, 0, time.FixedZone("x", 3600))
	hist, err := c.MeasureHistory(ctx, "demo:shop-api", []string{"coverage", "bugs"}, sonar.Ref{Branch: "release/1.0"}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 4 || hist[0].Metric != "alert_status" || len(hist[0].Points) != 6 || *hist[0].Points[2].Value != "ERROR" {
		t.Errorf("history = %+v", hist)
	}
	q := f.Calls("api/measures/search_history")[0].Query
	for k, want := range map[string]string{
		"component": "demo:shop-api", "metrics": "coverage,bugs", "branch": "release/1.0",
		"from": "2026-09-01T00:00:00+0000", "to": "2026-11-21T11:30:00+0000", "ps": "1000", "p": "1",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestMeasureHistoryPages(t *testing.T) {
	f := sonartest.New(t)
	f.Handlers["api/measures/search_history"] = func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("p")
		_, _ = fmt.Fprintf(w, `{"paging":{"pageIndex":%s,"pageSize":1000,"total":2500},"measures":[{"metric":"bugs","history":[{"date":"2026-01-0%sT00:00:00+0000","value":"%s"}]}]}`, page, page, page)
	}
	hist, err := client(t, f, sonar.Config{Token: "t"}).MeasureHistory(ctx, "p", []string{"bugs"}, sonar.Ref{}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Calls("api/measures/search_history")) != 3 || len(hist) != 1 || len(hist[0].Points) != 3 || *hist[0].Points[2].Value != "3" {
		t.Errorf("calls=%d history=%+v", len(f.Calls("api/measures/search_history")), hist)
	}
}

func TestMeasures(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})

	// 250 projects and 20 metrics: 3 project chunks x 2 metric chunks.
	projects := make([]string, 250)
	for i := range projects {
		projects[i] = fmt.Sprintf("p%d", i)
	}
	metrics := make([]string, 20)
	for i := range metrics {
		metrics[i] = fmt.Sprintf("m%d", i)
	}
	if _, err := c.Measures(ctx, projects, metrics, sonar.Ref{}); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls("api/measures/search")
	if len(calls) != 6 {
		t.Fatalf("%d requests, want 6", len(calls))
	}
	if n := len(strings.Split(calls[0].Query.Get("projectKeys"), ",")); n != 100 {
		t.Errorf("first chunk has %d projects", n)
	}
	if n := len(strings.Split(calls[0].Query.Get("metricKeys"), ",")); n != 15 {
		t.Errorf("first chunk has %d metrics", n)
	}

	// With a branch, each project is asked for on its own, and a "new code"
	// value is read from its period.
	ms, err := c.Measures(ctx, []string{"a", "b"}, []string{"coverage", "new_coverage"}, sonar.Ref{Branch: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Calls("api/measures/component")) != 2 || len(ms) != 4 {
		t.Fatalf("%d requests, %d measures", len(f.Calls("api/measures/component")), len(ms))
	}
	if ms[1].Component != "a" || ms[1].Metric != "new_coverage" || *ms[1].Val() != "12.0" || *ms[0].Val() != "61.5" {
		t.Errorf("measures = %+v", ms)
	}
	if got := f.Calls("api/measures/component")[0].Query.Get("branch"); got != "develop" {
		t.Errorf("branch = %q", got)
	}
}

func TestIssues(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	open := false
	filter := sonar.IssueFilter{
		Projects: []string{"demo:shop-api"}, Severities: []string{"BLOCKER", "CRITICAL"}, Types: []string{"BUG"},
		Resolved: &open, CreatedAfter: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}
	issues, total, err := c.Issues(ctx, filter, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 15 || len(issues) != 2 || issues[0].Rule != "python:S1764" || issues[0].File() != "src/checks.py" {
		t.Errorf("total=%d issues=%+v", total, issues)
	}
	q := f.Calls("api/issues/search")[0].Query
	for k, want := range map[string]string{
		"componentKeys": "demo:shop-api", "severities": "BLOCKER,CRITICAL", "types": "BUG", "resolved": "false",
		"createdAfter": "2026-11-01T00:00:00+0000", "s": "CREATION_DATE", "asc": "false", "ps": "2", "p": "1",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Has("createdBefore") || q.Has("organization") || q.Has("branch") {
		t.Errorf("unexpected parameters: %v", q)
	}

	counts, total, err := c.IssueCounts(ctx, sonar.IssueFilter{}, "severities")
	if err != nil || total != 15 || len(counts) != 5 || counts[0].Value != "MAJOR" || counts[0].Count != 10 {
		t.Errorf("counts=%+v total=%d err=%v", counts, total, err)
	}
	q = f.Calls("api/issues/search")[1].Query
	if q.Get("facets") != "severities" || q.Get("ps") != "1" || q.Has("componentKeys") {
		t.Errorf("count query = %v", q)
	}
}

// The API refuses to return results past the 10,000th; the client must stop
// asking before that instead of failing.
func TestIssuesStopAtTenThousand(t *testing.T) {
	f := sonartest.New(t)
	f.Handlers["api/issues/search"] = func(w http.ResponseWriter, r *http.Request) {
		var page, size int
		_, _ = fmt.Sscan(r.URL.Query().Get("p"), &page)
		_, _ = fmt.Sscan(r.URL.Query().Get("ps"), &size)
		if page*size > 10000 {
			sonartest.Error(w, http.StatusBadRequest, "Can return only the first 10000 results.")
			return
		}
		var b strings.Builder
		b.WriteString(`{"paging":{"total":25000},"issues":[`)
		for i := 0; i < size; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"key":"%d-%d"}`, page, i)
		}
		b.WriteString("]}")
		_, _ = w.Write([]byte(b.String()))
	}
	issues, total, err := client(t, f, sonar.Config{Token: "t"}).Issues(ctx, sonar.IssueFilter{}, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 10000 || total != 25000 || len(f.Calls("api/issues/search")) != 20 {
		t.Errorf("%d issues, total %d, %d requests", len(issues), total, len(f.Calls("api/issues/search")))
	}
}

func TestAnalysesAndBranches(t *testing.T) {
	f := sonartest.New(t)
	c := client(t, f, sonar.Config{Token: "t"})
	analyses, err := c.Analyses(ctx, "demo:shop-api", "", time.Time{}, time.Time{})
	if err != nil || len(analyses) != 6 || analyses[2].Events[0].Category != "QUALITY_GATE" || analyses[0].ProjectVersion != "2.2" {
		t.Errorf("analyses = %+v, %v", analyses, err)
	}
	branches, err := c.Branches(ctx, "demo:shop-api")
	if err != nil || len(branches) != 1 || !branches[0].IsMain || branches[0].Status.QualityGateStatus != "OK" {
		t.Errorf("branches = %+v, %v", branches, err)
	}
}

func TestParseTime(t *testing.T) {
	got, ok := sonar.ParseTime("2026-08-29T08:13:39+0200")
	if !ok || !got.Equal(time.Date(2026, 8, 29, 6, 13, 39, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("ParseTime = %v, %v", got, ok)
	}
	for _, bad := range []string{"", "2026-08-29", "yesterday"} {
		if _, ok := sonar.ParseTime(bad); ok {
			t.Errorf("ParseTime(%q) succeeded", bad)
		}
	}
}

func TestRequestsAreCancelled(t *testing.T) {
	f := sonartest.New(t)
	f.Handlers["api/metrics/search"] = func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	c, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := client(t, f, sonar.Config{Token: "t"}).Metrics(c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}
