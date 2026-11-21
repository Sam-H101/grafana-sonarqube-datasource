//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// hasHotspotRules reports whether the server under test still has security
// hotspot rules. SonarQube 9.9 does; Community Build 26.11 reports the same
// findings as vulnerabilities and has none.
func hasHotspotRules(t *testing.T, ds *plugin.Datasource) bool {
	t.Helper()
	return strings.Contains(health(t, ds).Message, "SonarQube Server 9.")
}

func notices(f *data.Frame) string {
	if f.Meta == nil {
		return ""
	}
	var out []string
	for _, n := range f.Meta.Notices {
		out = append(out, n.Text)
	}
	return strings.Join(out, " | ")
}

func TestQualityGateConditions(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 1, map[string]any{"queryType": "qualityGate"})[0]
	projects, statuses, gates := col(t, fr, "project"), col(t, fr, "status"), col(t, fr, "quality_gate")
	seen := map[string]bool{}
	failedLegacy := 0
	for i, p := range projects {
		seen[p] = true
		if p == "Legacy Billing" {
			if gates[i] != "ERROR" {
				t.Errorf("Legacy Billing's gate is %s", gates[i])
			}
			if statuses[i] == "ERROR" {
				failedLegacy++
			}
		}
	}
	if len(seen) != 3 {
		t.Errorf("conditions for %v, want all three projects", seen)
	}
	if failedLegacy == 0 {
		t.Error("Legacy Billing fails its gate, but no condition is marked failed")
	}
	// Failed conditions come first within a project.
	if statuses[0] != "ERROR" || projects[0] != "Legacy Billing" {
		t.Errorf("first row is %s %s", projects[0], statuses[0])
	}
	for i, req := range col(t, fr, "required") {
		if !strings.HasPrefix(req, "at least ") && !strings.HasPrefix(req, "at most ") && !strings.HasSuffix(req, " or better") {
			t.Errorf("row %d: required = %q", i, req)
		}
	}

	failed := frames(t, ds, 1, map[string]any{"queryType": "qualityGate", "failedOnly": true})[0]
	fs := col(t, failed, "status")
	if len(fs) == 0 || len(fs) >= len(statuses) {
		t.Errorf("%d failed conditions of %d", len(fs), len(statuses))
	}
	for _, s := range fs {
		if s != "ERROR" {
			t.Errorf("failed only returned a %s condition", s)
		}
	}

	one := frames(t, ds, 1, map[string]any{"queryType": "qualityGate", "projects": []string{"demo:shop-web"}})[0]
	for _, p := range col(t, one, "project") {
		if p != "Shop Web" {
			t.Errorf("asked for Shop Web, got %s", p)
		}
	}
	if n, _ := one.RowLen(); n == 0 {
		t.Error("no conditions for Shop Web")
	}
}

func TestProjectsByTag(t *testing.T) {
	ds := server(t)
	for tags, want := range map[string]string{
		"backend":         "demo:legacy-billing|demo:shop-api",
		"frontend":        "demo:shop-web",
		"frontend,legacy": "demo:legacy-billing|demo:shop-web",
		"SHOP":            "demo:shop-api|demo:shop-web",
	} {
		fr := frames(t, ds, 1, map[string]any{"queryType": "projects", "tags": strings.Split(tags, ",")})[0]
		if got := strings.Join(col(t, fr, "key"), "|"); got != want {
			t.Errorf("tags %s: %s, want %s", tags, got, want)
		}
	}
	none := frames(t, ds, 1, map[string]any{"queryType": "projects", "tags": []string{"no-such-tag"}})[0]
	if n, _ := none.RowLen(); n != 0 || !strings.Contains(notices(none), "No project the token can see has the tag no-such-tag.") {
		t.Errorf("unknown tag: %d rows, notices %q", n, notices(none))
	}
}

func TestHotspots(t *testing.T) {
	ds := server(t)
	q := func(status string, limit int, projects ...string) *data.Frame {
		m := map[string]any{"queryType": "hotspots", "projects": projects, "hotspotStatus": status}
		if limit > 0 {
			m["limit"] = limit
		}
		return frames(t, ds, 1, m)[0]
	}
	if !hasHotspotRules(t, ds) {
		fr := q("all", 0, "demo:legacy-billing", "demo:shop-web")
		if n, _ := fr.RowLen(); n != 0 || !strings.Contains(notices(fr), "No matching security hotspots.") {
			t.Errorf("%d hotspots, notices %q", n, notices(fr))
		}
		return
	}

	// The seed leaves Legacy Billing with three hotspots, one reviewed as
	// safe, and Shop Web with two.
	toReview := q("toReview", 0, "demo:legacy-billing")
	if got := strings.Join(col(t, toReview, "status"), " "); got != "TO_REVIEW TO_REVIEW" {
		t.Errorf("to review: %s", got)
	}
	reviewed := q("reviewed", 0, "demo:legacy-billing")
	if got := strings.Join(col(t, reviewed, "status"), " ") + "/" + strings.Join(col(t, reviewed, "resolution"), " "); got != "REVIEWED/SAFE" {
		t.Errorf("reviewed: %s", got)
	}
	all := q("all", 0, "demo:legacy-billing", "demo:shop-web")
	if n, _ := all.RowLen(); n != 5 {
		t.Errorf("all hotspots of two projects: %d", n)
	}
	if got := col(t, all, "file")[0]; got != "src/client.py" {
		t.Errorf("file = %s", got)
	}
	if link := col(t, all, "link")[0]; !strings.HasPrefix(link, sonarURL()+"/security_hotspots?id=demo%3A") || !strings.Contains(link, "&hotspots=") {
		t.Errorf("link = %s", link)
	}
	if got := col(t, all, "rule")[0]; got != "python:S1313" {
		t.Errorf("rule = %s", got)
	}
	limited := q("all", 2, "demo:legacy-billing", "demo:shop-web")
	if n, _ := limited.RowLen(); n != 2 || !strings.Contains(notices(limited), "Showing 2 of 5 matching hotspots") {
		t.Errorf("limit 2: %d rows, notices %q", n, notices(limited))
	}
	if r := query(t, ds, 1, map[string]any{"queryType": "hotspots", "projects": []string{"no:such"}}); r.Error == nil || r.ErrorSource != backend.ErrorSourceDownstream {
		t.Errorf("unknown project: %v", r.Error)
	}
}

// The servers in the test matrix are Community Builds, which have no pull
// request analysis.
func TestPullRequestsNeedAPaidEdition(t *testing.T) {
	ds := server(t)
	r := query(t, ds, 1, map[string]any{"queryType": "pullRequests", "projects": []string{"demo:shop-api"}})
	if r.Error == nil || !strings.Contains(r.Error.Error(), "does not analyse pull requests") || r.Status != backend.StatusBadRequest {
		t.Errorf("pull requests on Community Build: %v (%v)", r.Error, r.Status)
	}
	r = query(t, ds, 1, map[string]any{"queryType": "measures", "projects": []string{"demo:shop-api"}, "metrics": []string{"coverage"}, "pullRequest": "12"})
	if r.Error == nil || !strings.Contains(r.Error.Error(), "pull request '12' not found") {
		t.Errorf("a pull request on Community Build: %v", r.Error)
	}
}

// SonarQube Cloud has pull requests and hotspots; the public project below
// has had both for as long as these tests exist.
func TestCloudPullRequestsHotspotsAndTags(t *testing.T) {
	if os.Getenv("SONARCLOUD_TESTS") != "1" {
		t.Skip("set SONARCLOUD_TESTS=1 to test against sonarcloud.io (needs internet access)")
	}
	const project = "SonarSource_sonarqube-mcp-server"
	ds := datasource(t, "https://sonarcloud.io", `{"organization":"sonarsource"}`, "")

	prs := frames(t, ds, 1, map[string]any{"queryType": "pullRequests", "projects": []string{project}})[0]
	keys := col(t, prs, "key")
	if len(keys) == 0 {
		t.Fatal("no pull requests")
	}
	pr := keys[0]
	if link := col(t, prs, "link")[0]; link != "https://sonarcloud.io/dashboard?id="+project+"&pullRequest="+pr {
		t.Errorf("link = %s", link)
	}
	if got := col(t, prs, "project")[0]; got != "SonarQube MCP Server" {
		t.Errorf("project = %s", got)
	}

	// The same pull request through every query type that takes one.
	measures := frames(t, ds, 1, map[string]any{"queryType": "measures", "projects": []string{project}, "metrics": []string{"new_lines", "alert_status"}, "pullRequest": pr})[0]
	if n, _ := measures.RowLen(); n != 1 {
		t.Errorf("measures of pull request %s: %d rows", pr, n)
	}
	history := frames(t, ds, 365, map[string]any{"queryType": "measureHistory", "projects": []string{project}, "metrics": []string{"new_lines"}, "pullRequest": pr})
	if len(history) != 1 {
		t.Errorf("history of pull request %s: %d frames", pr, len(history))
	}
	for _, typ := range []string{"issues", "issueCounts", "hotspots"} {
		if r := query(t, ds, 1, map[string]any{"queryType": typ, "projects": []string{project}, "pullRequest": pr}); r.Error != nil {
			t.Errorf("%s of pull request %s: %v", typ, pr, r.Error)
		}
	}
	gate := frames(t, ds, 1, map[string]any{"queryType": "qualityGate", "projects": []string{project}, "pullRequest": pr})[0]
	if n, _ := gate.RowLen(); n == 0 {
		t.Errorf("no quality gate conditions for pull request %s", pr)
	}
	if r := query(t, ds, 1, map[string]any{"queryType": "measures", "projects": []string{project}, "metrics": []string{"coverage"}, "pullRequest": "999999999"}); r.Error == nil {
		t.Error("an unknown pull request gave no error")
	}

	// Quality gate conditions for the whole organization, in few requests.
	org := frames(t, ds, 1, map[string]any{"queryType": "qualityGate"})[0]
	if n, _ := org.RowLen(); n < 50 {
		t.Errorf("quality gate conditions for the organization: %d rows", n)
	}

	hotspots := frames(t, ds, 1, map[string]any{"queryType": "hotspots", "projects": []string{project}, "hotspotStatus": "all"})[0]
	if n, _ := hotspots.RowLen(); n == 0 {
		t.Fatal("no hotspots")
	}
	if link := col(t, hotspots, "link")[0]; !strings.HasPrefix(link, "https://sonarcloud.io/security_hotspots?id="+project+"&hotspots=") {
		t.Errorf("link = %s", link)
	}
	reviewed := frames(t, ds, 1, map[string]any{"queryType": "hotspots", "projects": []string{project}, "hotspotStatus": "reviewed"})[0]
	for _, s := range col(t, reviewed, "status") {
		if s != "REVIEWED" {
			t.Errorf("reviewed hotspots include %s", s)
		}
	}

	tagged := frames(t, ds, 1, map[string]any{"queryType": "projects", "tags": []string{"server-platform", "dotnet"}})[0]
	tags := col(t, tagged, "tags")
	if len(tags) == 0 {
		t.Fatal("no projects tagged server-platform or dotnet")
	}
	for _, tg := range tags {
		if !strings.Contains(tg, "server-platform") && !strings.Contains(tg, "dotnet") {
			t.Errorf("a project with tags %q matched", tg)
		}
	}
}
