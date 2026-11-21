package plugin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar/sonartest"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func noticesOf(f *data.Frame) string {
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
	ds, f := serverDS(t)
	fr := frames(t, ds, `{"queryType":"qualityGate"}`)[0]
	// Every project, in name order; within a project the failed condition first.
	rows := make([]string, 0)
	project, status, value, required, metric := col(t, fr, "project"), col(t, fr, "status"), col(t, fr, "value"), col(t, fr, "required"), col(t, fr, "metric")
	for i := range project {
		rows = append(rows, strings.Join([]string{project[i], metric[i], status[i], value[i], required[i]}, ","))
	}
	want := []string{
		"Legacy Billing,new_violations,ERROR,1,at most 0",
		"Legacy Billing,new_coverage,OK,0%,at least 80%",
		"Legacy Billing,new_duplicated_lines_density,OK,0,at most 3",
		"Shop API,new_violations,OK,0,at most 0",
		"Shop Web,new_violations,ERROR,2,at most 0",
		"Shop Web,new_coverage,OK,10.5%,at least 80%",
		"Shop Web,new_duplicated_lines_density,OK,0,at most 3",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("conditions:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if got := strings.Join(col(t, fr, "quality_gate"), " "); got != "ERROR ERROR ERROR OK ERROR ERROR ERROR" {
		t.Errorf("gates = %s", got)
	}
	// Statuses read as Passed/Failed, and the project links to SonarQube.
	if sf := field(t, fr, "status"); len(sf.Config.Mappings) != 1 {
		t.Errorf("status mappings = %v", sf.Config.Mappings)
	}
	if links := field(t, fr, "project").Config.Links; len(links) != 1 || links[0].URL != f.URL+"/dashboard?id=${__data.fields.key:percentencode}" {
		t.Errorf("links = %+v", links)
	}
	// One request covers all three projects.
	calls := f.Calls("api/measures/search")
	if len(calls) != 1 || calls[0].Query.Get("metricKeys") != "quality_gate_details" || calls[0].Query.Get("projectKeys") != "demo:legacy-billing,demo:shop-api,demo:shop-web" {
		t.Errorf("requests = %+v", calls)
	}

	failed := frames(t, ds, `{"queryType":"qualityGate","failedOnly":true}`)[0]
	if got := strings.Join(col(t, failed, "project"), "|") + " " + strings.Join(col(t, failed, "status"), "|"); got != "Legacy Billing|Shop Web ERROR|ERROR" {
		t.Errorf("failed only = %s", got)
	}
	passing := frames(t, ds, `{"queryType":"qualityGate","failedOnly":true,"projects":["demo:shop-api"]}`)[0]
	if n, _ := passing.RowLen(); n != 0 || !strings.Contains(noticesOf(passing), "No condition failed") {
		t.Errorf("a passing gate with failed only: %d rows, %q", n, noticesOf(passing))
	}

	// A pull request is read per project, and its link goes to that pull request.
	f.Reset()
	pr := frames(t, ds, `{"queryType":"qualityGate","projects":["demo:shop-api"],"pullRequest":"12"}`)[0]
	if got := strings.Join(col(t, pr, "status"), " ") + "/" + strings.Join(col(t, pr, "value"), "|") + "/" + strings.Join(col(t, pr, "required"), "|"); got != "ERROR OK/61.5%|/at least 80%|A or better" {
		t.Errorf("pull request conditions = %s", got)
	}
	if q := f.Calls("api/measures/component")[0].Query; q.Get("pullRequest") != "12" || q.Has("branch") {
		t.Errorf("pull request query = %v", q)
	}
	if links := field(t, pr, "project").Config.Links; !strings.HasSuffix(links[0].URL, "&pullRequest=12") {
		t.Errorf("links = %+v", links)
	}
}

func TestFormatValueAndRequirement(t *testing.T) {
	rating, percent, count, debt := sonar.Metric{Type: "RATING"}, sonar.Metric{Type: "PERCENT"}, sonar.Metric{Type: "INT"}, sonar.Metric{Type: "WORK_DUR"}
	for _, c := range []struct {
		m       sonar.Metric
		op, thr string
		actual  string
		value   string
		req     string
	}{
		{rating, "GT", "1", "4", "D", "A or better"},
		{rating, "GT", "3", "3.0", "C", "C or better"},
		{percent, "LT", "80", "79.95", "79.95%", "at least 80%"},
		{percent, "GT", "3", "0.0", "0%", "at most 3%"},
		{count, "GT", "0", "12", "12", "at most 0"},
		{debt, "GT", "60", "125", "125 min", "at most 60 min"},
		{count, "GT", "0", "", "", "at most 0"},
		{sonar.Metric{}, "LT", "x", "words", "words", "at least x"},
	} {
		if got := formatValue(c.m, c.actual); got != c.value {
			t.Errorf("formatValue(%s, %q) = %q, want %q", c.m.Type, c.actual, got, c.value)
		}
		if got := requirement(c.m, c.op, c.thr); got != c.req {
			t.Errorf("requirement(%s, %s, %q) = %q, want %q", c.m.Type, c.op, c.thr, got, c.req)
		}
	}
}

func TestHotspots(t *testing.T) {
	ds, f := serverDS(t)
	fr := frames(t, ds, `{"queryType":"hotspots","projects":["demo:legacy-billing"],"hotspotStatus":"all"}`)[0]
	if got := strings.Join(col(t, fr, "status"), " ") + "/" + strings.Join(col(t, fr, "resolution"), "|"); got != "REVIEWED TO_REVIEW TO_REVIEW/SAFE||" {
		t.Errorf("hotspots = %s", got)
	}
	if got := col(t, fr, "project")[0] + "," + col(t, fr, "file")[0] + "," + col(t, fr, "line")[0] + "," + col(t, fr, "rule")[0] + "," + col(t, fr, "priority")[0] + "," + col(t, fr, "category")[0]; got != "Legacy Billing,src/client.py,13,python:S1313,LOW,Others" {
		t.Errorf("first hotspot = %s", got)
	}
	if link := col(t, fr, "link")[0]; !strings.HasPrefix(link, f.URL+"/security_hotspots?id=demo%3Alegacy-billing&hotspots=") {
		t.Errorf("link = %s", link)
	}
	if pf := field(t, fr, "priority"); len(pf.Config.Mappings) != 1 {
		t.Errorf("priority mappings = %v", pf.Config.Mappings)
	}
	// Both names of the project parameter are sent; no status means both.
	q := f.Calls("api/hotspots/search")[0].Query
	if q.Get("project") != "demo:legacy-billing" || q.Get("projectKey") != "demo:legacy-billing" || q.Has("status") || q.Get("ps") != "100" {
		t.Errorf("query = %v", q)
	}

	f.Reset()
	frames(t, ds, `{"queryType":"hotspots","projects":["demo:legacy-billing"],"branch":"release/1.0","hotspotStatus":"reviewed","limit":7}`)
	q = f.Calls("api/hotspots/search")[0].Query
	if q.Get("status") != "REVIEWED" || q.Get("branch") != "release/1.0" || q.Get("ps") != "7" {
		t.Errorf("filtered query = %v", q)
	}
	f.Reset()
	frames(t, ds, `{"queryType":"hotspots","projects":["demo:legacy-billing"]}`)
	if q = f.Calls("api/hotspots/search")[0].Query; q.Get("status") != "TO_REVIEW" {
		t.Errorf("default status = %q", q.Get("status"))
	}

	// Several projects are merged, and the limit applies to the whole list.
	limited := frames(t, ds, `{"queryType":"hotspots","projects":["demo:legacy-billing","demo:shop-web"],"hotspotStatus":"all","limit":4}`)[0]
	if n, _ := limited.RowLen(); n != 4 || !strings.Contains(noticesOf(limited), "Showing 4 of 6 matching hotspots") {
		t.Errorf("limited: %d rows, %q", n, noticesOf(limited))
	}

	// No hotspots at all is explained (Community Build 26.11 has no hotspot rules).
	f.Handlers["api/hotspots/search"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paging":{"pageIndex":1,"pageSize":100,"total":0},"hotspots":[],"components":[]}`))
	}
	empty := frames(t, ds, `{"queryType":"hotspots","projects":["demo:shop-api"]}`)[0]
	if n, _ := empty.RowLen(); n != 0 || !strings.Contains(noticesOf(empty), "No matching security hotspots.") {
		t.Errorf("empty: %d rows, %q", n, noticesOf(empty))
	}
}

func TestHotspotCategoryNames(t *testing.T) {
	for slug, want := range map[string]string{"auth": "Authentication", "weak-cryptography": "Weak cryptography", "sql-injection": "SQL injection", "others": "Others", "": ""} {
		if got := categoryName(slug); got != want {
			t.Errorf("categoryName(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestPullRequests(t *testing.T) {
	// A server without pull request analysis says what is needed.
	ds, _ := serverDS(t)
	r := run(t, ds, `{"queryType":"pullRequests","projects":["demo:shop-api"]}`)
	if r.Error == nil || r.Error.Error() != "This SonarQube does not analyse pull requests. That needs SonarQube Developer Edition or higher, or SonarQube Cloud." || r.Status != backend.StatusBadRequest || r.ErrorSource != backend.ErrorSourceDownstream {
		t.Errorf("community: %v (%v, %v)", r.Error, r.Status, r.ErrorSource)
	}

	f := sonartest.New(t)
	cloud := cloudDS(t, f, "sonarsource", "")
	fr := frames(t, cloud, `{"queryType":"pullRequests","projects":["SonarSource_echoes-react"]}`)[0]
	// Most recently analysed first.
	if got := strings.Join(col(t, fr, "key"), " ") + "/" + strings.Join(col(t, fr, "quality_gate"), " ") + "/" + strings.Join(col(t, fr, "code_smells"), " "); got != "763 767/OK ERROR/0 1" {
		t.Errorf("pull requests = %s", got)
	}
	if got := col(t, fr, "branch")[1] + "," + col(t, fr, "target")[1] + "," + col(t, fr, "url")[1]; got != "feature/mcp-workspace-env,master,https://github.com/SonarSource/sonarqube-mcp-server/pull/767" {
		t.Errorf("second pull request = %s", got)
	}
	if got := col(t, fr, "link")[0]; got != f.URL+"/dashboard?id=SonarSource_echoes-react&pullRequest=763" {
		t.Errorf("link = %s", got)
	}
	// The variable support reads these two fields.
	field(t, fr, "title")
	if gf := field(t, fr, "quality_gate"); len(gf.Config.Mappings) != 1 {
		t.Errorf("gate mappings = %v", gf.Config.Mappings)
	}

	// A pull request reaches the requests of the other query types.
	f.Reset()
	frames(t, cloud, `{"queryType":"measureHistory","projects":["SonarSource_echoes-react"],"metrics":["coverage"],"pullRequest":"763"}`)
	frames(t, cloud, `{"queryType":"issues","projects":["SonarSource_echoes-react"],"pullRequest":"763"}`)
	frames(t, cloud, `{"queryType":"issueCounts","pullRequest":"763"}`)
	frames(t, cloud, `{"queryType":"hotspots","projects":["SonarSource_echoes-react"],"pullRequest":"763"}`)
	for _, path := range []string{"api/measures/search_history", "api/issues/search", "api/hotspots/search"} {
		for _, c := range f.Calls(path) {
			if c.Query.Get("pullRequest") != "763" || c.Query.Has("branch") {
				t.Errorf("%s: %v", path, c.Query)
			}
		}
		if len(f.Calls(path)) == 0 {
			t.Errorf("no request to %s", path)
		}
	}
	// Links from an issue of a pull request open it in that pull request.
	issues := frames(t, cloud, `{"queryType":"issues","projects":["SonarSource_echoes-react"],"resolution":"all","limit":1,"pullRequest":"763"}`)[0]
	if link := col(t, issues, "link")[0]; !strings.HasSuffix(link, "&pullRequest=763") {
		t.Errorf("issue link = %s", link)
	}
}

func TestProjectsByTag(t *testing.T) {
	ds, f := serverDS(t)
	frames(t, ds, `{"queryType":"projects"}`)
	f.Reset()
	fr := frames(t, ds, `{"queryType":"projects","tags":["Backend","legacy"]}`)[0]
	calls := f.Calls("api/components/search_projects")
	// A tag filter is not answered from the cached list of all projects.
	if len(calls) != 1 || calls[0].Query.Get("filter") != "tags IN (backend,legacy)" {
		t.Fatalf("requests = %+v", calls)
	}
	if n, _ := fr.RowLen(); n != 3 {
		t.Errorf("%d rows", n)
	}

	f.Handlers["api/components/search_projects"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paging":{"pageIndex":1,"pageSize":500,"total":0},"components":[]}`))
	}
	none := frames(t, ds, `{"queryType":"projects","tags":["nope","other"]}`)[0]
	if got := noticesOf(none); got != "No project the token can see has the tag nope or other." {
		t.Errorf("notice = %q", got)
	}
}

func TestTagsResource(t *testing.T) {
	ds, _ := serverDS(t)
	var status int
	var body string
	err := ds.CallResource(context.Background(), &backend.CallResourceRequest{Method: http.MethodGet, Path: "tags", URL: "tags"},
		backend.CallResourceResponseSenderFunc(func(r *backend.CallResourceResponse) error {
			status, body = r.Status, string(r.Body)
			return nil
		}))
	if err != nil || status != 200 || strings.TrimSpace(body) != `["backend","frontend","legacy","shop"]` {
		t.Errorf("tags: %d %s (%v)", status, body, err)
	}
}
