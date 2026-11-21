//go:build integration

// Package integration runs the plugin against real servers: the dev stack's
// SonarQube with its seeded projects (devenv/sonarqube/seed.py), and, when
// SONARCLOUD_TESTS=1, the public projects of the "sonarsource" organization
// on SonarQube Cloud.
//
//	SONAR_URL         default http://sonarqube:9000
//	SONAR_TOKEN_FILE  default /run/sonar/token (or SONAR_TOKEN)
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func sonarURL() string { return env("SONAR_URL", "http://sonarqube:9000") }

func token(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("SONAR_TOKEN"); v != "" {
		return v
	}
	b, err := os.ReadFile(env("SONAR_TOKEN_FILE", "/run/sonar/token"))
	if err != nil {
		t.Fatalf("no token: set SONAR_TOKEN or SONAR_TOKEN_FILE (%v)", err)
	}
	return strings.TrimSpace(string(b))
}

func datasource(t *testing.T, url, jsonData, tok string) *plugin.Datasource {
	t.Helper()
	inst, err := plugin.NewDatasource(context.Background(), backend.DataSourceInstanceSettings{
		URL: url, JSONData: json.RawMessage(jsonData), DecryptedSecureJSONData: map[string]string{"token": tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*plugin.Datasource)
}

func server(t *testing.T) *plugin.Datasource { return datasource(t, sonarURL(), `{}`, token(t)) }

func query(t *testing.T, ds *plugin.Datasource, days int, q map[string]any) backend.DataResponse {
	t.Helper()
	js, _ := json.Marshal(q)
	now := time.Now()
	resp, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{{
		RefID: "A", JSON: js, TimeRange: backend.TimeRange{From: now.AddDate(0, 0, -days), To: now},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Responses["A"]
}

func frames(t *testing.T, ds *plugin.Datasource, days int, q map[string]any) data.Frames {
	t.Helper()
	r := query(t, ds, days, q)
	if r.Error != nil {
		t.Fatalf("%v: %v", q, r.Error)
	}
	return r.Frames
}

func col(t *testing.T, f *data.Frame, name string) []string {
	t.Helper()
	for _, fld := range f.Fields {
		if fld.Name != name {
			continue
		}
		out := make([]string, fld.Len())
		for i := range out {
			if v, ok := fld.ConcreteAt(i); ok {
				out[i] = fmt.Sprint(v)
			} else {
				out[i] = "null"
			}
		}
		return out
	}
	t.Fatalf("no field %q", name)
	return nil
}

func health(t *testing.T, ds *plugin.Datasource) *backend.CheckHealthResult {
	t.Helper()
	h, err := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHealth(t *testing.T) {
	tok := token(t)
	// A web server that is not SonarQube, as a mistyped address would reach.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>Intranet</title>"))
	}))
	defer other.Close()
	cases := []struct {
		name, url, json, token, want string
		ok                           bool
	}{
		{"ok", sonarURL(), `{}`, tok, "Connected to SonarQube Server ", true},
		{"ok, pasted API address", sonarURL() + "/api/", `{}`, tok, "The token can see 3 projects.", true},
		{"wrong token", sonarURL(), `{}`, "squ_0000000000000000000000000000000000000000", "SonarQube rejected the token.", false},
		{"no token", sonarURL(), `{}`, "", "This SonarQube requires authentication.", false},
		{"not sonarqube", other.URL, `{}`, tok, "did not answer like SonarQube", false},
		{"unreachable", "http://127.0.0.1:1", `{}`, tok, "Cannot reach SonarQube at http://127.0.0.1:1", false},
		{"no such host", "http://no-such-sonarqube.invalid:9000", `{}`, tok, "host not found", false},
		{"https on a plain port", strings.Replace(sonarURL(), "http://", "https://", 1), `{}`, tok, "answered with plain HTTP", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := health(t, datasource(t, c.url, c.json, c.token))
			if (h.Status == backend.HealthStatusOk) != c.ok || !strings.Contains(h.Message, c.want) {
				t.Fatalf("status=%v message=%q", h.Status, h.Message)
			}
			if c.token != "" && strings.Contains(h.Message, c.token) {
				t.Errorf("the message contains the token: %q", h.Message)
			}
		})
	}
}

// The seed analyses Shop API six times; its bug counts are 6, 5, 7, 3, 2, 1
// and its coverage rises each time.
func TestMeasureHistory(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 90, map[string]any{"queryType": "measureHistory", "projects": []string{"demo:shop-api"}, "metrics": []string{"bugs", "coverage", "alert_status"}})
	if len(fr) != 3 {
		t.Fatalf("%d frames", len(fr))
	}
	if got := strings.Join(col(t, fr[0], "bugs"), " "); got != "6 5 7 3 2 1" {
		t.Errorf("bugs = %s", got)
	}
	cov := col(t, fr[1], "coverage")
	first, _ := strconv.ParseFloat(cov[0], 64)
	last, _ := strconv.ParseFloat(cov[len(cov)-1], 64)
	if len(cov) != 6 || first >= last || first < 20 || last > 95 {
		t.Errorf("coverage = %v", cov)
	}
	for _, v := range col(t, fr[2], "alert_status") {
		if v != "OK" && v != "ERROR" {
			t.Errorf("quality gate value %q", v)
		}
	}

	// The time range limits the history: analyses 28, 14 and 3 days ago.
	fr = frames(t, ds, 30, map[string]any{"queryType": "measureHistory", "projects": []string{"demo:shop-api"}, "metrics": []string{"bugs"}})
	if got := strings.Join(col(t, fr[0], "bugs"), " "); got != "3 2 1" {
		t.Errorf("bugs in the last 30 days = %s", got)
	}

	// Several projects: one series each, named by project.
	fr = frames(t, ds, 90, map[string]any{"queryType": "measureHistory", "projects": []string{"demo:shop-web,demo:legacy-billing"}, "metrics": []string{"coverage"}})
	if len(fr) != 2 || fr[0].Fields[1].Config.DisplayNameFromDS != "Shop Web" || fr[1].Fields[1].Config.DisplayNameFromDS != "Legacy Billing" {
		t.Errorf("series: %d", len(fr))
	}
	if n := fr[0].Fields[0].Len(); n != 4 {
		t.Errorf("Shop Web has %d analyses, want 4", n)
	}
}

func TestMeasures(t *testing.T) {
	fr := frames(t, server(t), 90, map[string]any{"queryType": "measures", "metrics": []string{"alert_status", "coverage", "bugs", "ncloc", "reliability_rating", "sqale_index"}})[0]
	if got := strings.Join(col(t, fr, "project"), "|"); got != "Legacy Billing|Shop API|Shop Web" {
		t.Fatalf("projects = %s", got)
	}
	if got := strings.Join(col(t, fr, "bugs"), "|"); got != "9|1|4" {
		t.Errorf("bugs = %s", got)
	}
	legacy, _ := strconv.ParseFloat(col(t, fr, "coverage")[0], 64)
	api, _ := strconv.ParseFloat(col(t, fr, "coverage")[1], 64)
	if legacy > 25 || api < 70 {
		t.Errorf("coverage: legacy %v, api %v", legacy, api)
	}
	for _, name := range []string{"ncloc", "reliability_rating", "sqale_index"} {
		for _, v := range col(t, fr, name) {
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				t.Errorf("%s value %q is not a number", name, v)
			}
		}
	}
	if got := col(t, fr, "alert_status")[0]; got != "ERROR" {
		t.Errorf("Legacy Billing's quality gate = %s", got)
	}
}

func TestIssues(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 90, map[string]any{"queryType": "issues", "projects": []string{"demo:legacy-billing"}, "limit": 500})[0]
	n, _ := fr.RowLen()
	if n < 10 {
		t.Fatalf("%d open issues in Legacy Billing", n)
	}
	for i, s := range col(t, fr, "status") {
		if strings.Contains(s, "CLOSED") || strings.Contains(s, "RESOLVED") {
			t.Errorf("row %d is not open: %s", i, s)
		}
	}
	created := col(t, fr, "created")
	for i := 1; i < len(created); i++ {
		if created[i] > created[i-1] {
			t.Errorf("issues are not newest first: %s after %s", created[i], created[i-1])
			break
		}
	}
	if link := col(t, fr, "link")[0]; !strings.HasPrefix(link, sonarURL()+"/project/issues?id=demo%3Alegacy-billing&issues=") {
		t.Errorf("link = %s", link)
	}

	// Filters reach the server.
	fr = frames(t, ds, 90, map[string]any{"queryType": "issues", "types": []string{"BUG"}, "resolution": "all", "limit": 500})[0]
	for _, v := range col(t, fr, "type") {
		if v != "BUG" {
			t.Fatalf("type filter let %s through", v)
		}
	}
	all, _ := fr.RowLen()
	fr = frames(t, ds, 90, map[string]any{"queryType": "issues", "types": []string{"BUG"}, "resolution": "resolved", "limit": 500})[0]
	resolved, _ := fr.RowLen()
	fr = frames(t, ds, 90, map[string]any{"queryType": "issues", "types": []string{"BUG"}, "resolution": "open", "limit": 500})[0]
	open, _ := fr.RowLen()
	if resolved == 0 || open == 0 || resolved+open != all {
		t.Errorf("bugs: %d resolved + %d open != %d in all", resolved, open, all)
	}

	// Created in the time range: the last 5 days hold only the newest analyses.
	fr = frames(t, ds, 5, map[string]any{"queryType": "issues", "resolution": "all", "createdInRange": true, "limit": 500})[0]
	recent, _ := fr.RowLen()
	fr = frames(t, ds, 90, map[string]any{"queryType": "issues", "resolution": "all", "createdInRange": true, "limit": 500})[0]
	everything, _ := fr.RowLen()
	if recent == 0 || recent >= everything {
		t.Errorf("%d issues created in 5 days, %d in 90", recent, everything)
	}

	// A limit below the total says so.
	r := query(t, ds, 90, map[string]any{"queryType": "issues", "resolution": "all", "limit": 2})
	if n, _ := r.Frames[0].RowLen(); n != 2 || len(r.Frames[0].Meta.Notices) != 1 {
		t.Errorf("limit 2: %d rows, notices %+v", n, r.Frames[0].Meta)
	}
}

func TestIssueCounts(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 90, map[string]any{"queryType": "issueCounts", "groupBy": "severities"})[0]
	if got := strings.Join(col(t, fr, "severity"), "|"); got != "BLOCKER|CRITICAL|MAJOR|MINOR|INFO" {
		t.Errorf("severities = %s", got)
	}
	sum := 0
	for _, v := range col(t, fr, "issues") {
		n, _ := strconv.Atoi(v)
		sum += n
	}
	list := frames(t, ds, 90, map[string]any{"queryType": "issues", "limit": 10000})[0]
	if n, _ := list.RowLen(); n != sum || sum == 0 {
		t.Errorf("counts by severity add up to %d, the issue list has %d", sum, n)
	}

	fr = frames(t, ds, 90, map[string]any{"queryType": "issueCounts", "groupBy": "projects"})[0]
	if got := col(t, fr, "project")[0]; got != "Legacy Billing" {
		t.Errorf("the project with most issues = %s", got)
	}
	// Every grouping the editor offers is accepted by this server.
	for _, g := range []string{"types", "rules", "tags", "languages", "assignees", "author", "resolutions", "statuses"} {
		if r := query(t, ds, 90, map[string]any{"queryType": "issueCounts", "groupBy": g, "resolution": "all"}); r.Error != nil {
			t.Errorf("group by %s: %v", g, r.Error)
		}
	}
}

func TestAnalyses(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 90, map[string]any{"queryType": "analyses", "projects": []string{"demo:shop-api"}, "categories": []string{"VERSION"}})[0]
	if got := strings.Join(col(t, fr, "version"), " "); got != "2.2 2.1 2.0 1.2 1.1 1.0" {
		t.Errorf("versions = %s", got)
	}
	if got := col(t, fr, "text")[0]; got != "Version 2.2" {
		t.Errorf("event text = %s", got)
	}
	// Quality gate changes exist in the seeded history.
	fr = frames(t, ds, 90, map[string]any{"queryType": "analyses", "projects": []string{"demo:shop-api", "demo:shop-web", "demo:legacy-billing"}, "categories": []string{"QUALITY_GATE"}})[0]
	n, _ := fr.RowLen()
	if n == 0 {
		t.Fatal("no quality gate events")
	}
	for _, text := range col(t, fr, "text") {
		if !strings.HasPrefix(text, "Quality gate: ") {
			t.Errorf("event text = %q", text)
		}
	}
	// The time range applies.
	fr = frames(t, ds, 10, map[string]any{"queryType": "analyses", "projects": []string{"demo:shop-api"}})[0]
	if got := strings.Join(col(t, fr, "version"), " "); got != "2.2" {
		t.Errorf("analyses in the last 10 days = %s", got)
	}
}

func TestLists(t *testing.T) {
	ds := server(t)
	fr := frames(t, ds, 90, map[string]any{"queryType": "projects"})[0]
	if got := strings.Join(col(t, fr, "key"), "|"); got != "demo:legacy-billing|demo:shop-api|demo:shop-web" {
		t.Errorf("projects = %s", got)
	}
	fr = frames(t, ds, 90, map[string]any{"queryType": "metrics"})[0]
	keys := strings.Join(col(t, fr, "key"), " ")
	for _, want := range []string{"coverage", "bugs", "alert_status", "sqale_index"} {
		if !strings.Contains(" "+keys+" ", " "+want+" ") {
			t.Errorf("metric %s is missing", want)
		}
	}
	fr = frames(t, ds, 90, map[string]any{"queryType": "branches", "projects": []string{"demo:shop-api"}})[0]
	if got := col(t, fr, "main")[0]; got != "true" {
		t.Errorf("main = %s", got)
	}
}

func TestErrorsFromTheServer(t *testing.T) {
	ds := server(t)
	for name, c := range map[string]struct {
		q    map[string]any
		want string
	}{
		"unknown project": {map[string]any{"projects": []string{"no:such"}, "metrics": []string{"coverage"}}, "SonarQube: Component key 'no:such' not found"},
		"unknown metric":  {map[string]any{"projects": []string{"demo:shop-api"}, "metrics": []string{"not_a_metric"}}, "SonarQube: Metrics not_a_metric are not found"},
		"unknown branch":  {map[string]any{"projects": []string{"demo:shop-api"}, "metrics": []string{"coverage"}, "branch": "nope"}, "on branch 'nope' not found"},
	} {
		r := query(t, ds, 90, c.q)
		if r.Error == nil || !strings.Contains(r.Error.Error(), c.want) || r.ErrorSource != backend.ErrorSourceDownstream {
			t.Errorf("%s: %v (%v)", name, r.Error, r.ErrorSource)
		}
	}
}

// SonarQube Cloud, anonymously: the public projects of one organization.
func TestCloud(t *testing.T) {
	if os.Getenv("SONARCLOUD_TESTS") != "1" {
		t.Skip("set SONARCLOUD_TESTS=1 to test against sonarcloud.io (needs internet access)")
	}
	const project = "SonarSource_echoes-react"
	ds := datasource(t, "https://sonarcloud.io", `{"organization":"sonarsource"}`, "")

	h := health(t, ds)
	if h.Status != backend.HealthStatusOk || !strings.HasPrefix(h.Message, "Connected to SonarQube Cloud, organization sonarsource. Anonymous access can see ") {
		t.Fatalf("health: %+v", h)
	}
	h = health(t, datasource(t, "https://sonarcloud.io", `{"organization":"no-such-organization-zzz"}`, ""))
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "No organization for key") {
		t.Errorf("wrong organization: %+v", h)
	}
	h = health(t, datasource(t, "https://sonarcloud.io", `{"organization":"sonarsource"}`, "0000000000000000000000000000000000000000"))
	if h.Status != backend.HealthStatusError || !strings.HasPrefix(h.Message, "SonarQube Cloud rejected the token.") {
		t.Errorf("wrong token: %+v", h)
	}

	fr := frames(t, ds, 365, map[string]any{"queryType": "measureHistory", "projects": []string{project}, "metrics": []string{"coverage", "alert_status"}})
	if len(fr) != 2 || fr[0].Fields[0].Len() < 5 {
		t.Fatalf("history: %d frames", len(fr))
	}
	// Branches exist on Cloud: the same history on the main branch by name.
	onBranch := frames(t, ds, 365, map[string]any{"queryType": "measureHistory", "projects": []string{project}, "metrics": []string{"coverage"}, "branch": "main"})
	if onBranch[0].Fields[0].Len() != fr[0].Fields[0].Len() {
		t.Errorf("branch main has %d points, the default %d", onBranch[0].Fields[0].Len(), fr[0].Fields[0].Len())
	}

	all := frames(t, ds, 90, map[string]any{"queryType": "measures", "metrics": []string{"alert_status", "coverage"}})[0]
	if n, _ := all.RowLen(); n < 20 {
		t.Errorf("measures for every project: %d rows", n)
	}
	issues := frames(t, ds, 90, map[string]any{"queryType": "issues", "projects": []string{project}, "resolution": "all", "limit": 5})[0]
	if n, _ := issues.RowLen(); n == 0 {
		t.Error("no issues")
	}
	if link := col(t, issues, "link")[0]; !strings.HasPrefix(link, "https://sonarcloud.io/project/issues?id="+project) {
		t.Errorf("link = %s", link)
	}
	// Without projects, issue counts cover the organization.
	counts := frames(t, ds, 90, map[string]any{"queryType": "issueCounts", "groupBy": "severities", "resolution": "all"})[0]
	if n, _ := counts.RowLen(); n != 5 {
		t.Errorf("severity counts: %d rows", n)
	}
	// Every grouping the editor offers is accepted by Cloud too.
	for _, g := range []string{"types", "projects", "rules", "tags", "languages", "assignees", "author", "resolutions", "statuses"} {
		if r := query(t, ds, 90, map[string]any{"queryType": "issueCounts", "groupBy": g, "resolution": "all", "projects": []string{project}}); r.Error != nil {
			t.Errorf("group by %s: %v", g, r.Error)
		}
	}
	analyses := frames(t, ds, 365, map[string]any{"queryType": "analyses", "projects": []string{project}, "categories": []string{"VERSION"}})[0]
	if n, _ := analyses.RowLen(); n == 0 {
		t.Error("no version events")
	}
	branches := frames(t, ds, 90, map[string]any{"queryType": "branches", "projects": []string{project}})[0]
	if got := col(t, branches, "name")[0]; got != "main" {
		t.Errorf("branch = %s", got)
	}
}
