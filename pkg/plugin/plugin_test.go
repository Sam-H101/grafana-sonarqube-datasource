package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar/sonartest"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const testToken = "squ_test"

// newDS builds a data source through the real factory, pointed at a fake.
func newDS(t *testing.T, f *sonartest.Fake, jsonData, token string) *Datasource {
	t.Helper()
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{
		URL: f.URL, JSONData: json.RawMessage(jsonData), DecryptedSecureJSONData: map[string]string{"token": token},
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(*Datasource)
}

func serverDS(t *testing.T) (*Datasource, *sonartest.Fake) {
	f := sonartest.New(t)
	f.Token = testToken
	return newDS(t, f, `{}`, testToken), f
}

func run(t *testing.T, ds *Datasource, query string) backend.DataResponse {
	t.Helper()
	resp, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{{
		RefID: "A", JSON: json.RawMessage(query),
		TimeRange: backend.TimeRange{From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Responses["A"]
}

func frames(t *testing.T, ds *Datasource, query string) data.Frames {
	t.Helper()
	r := run(t, ds, query)
	if r.Error != nil {
		t.Fatalf("query %s: %v", query, r.Error)
	}
	for _, f := range r.Frames {
		if f.RefID != "A" {
			t.Errorf("frame %q has RefID %q", f.Name, f.RefID)
		}
	}
	return r.Frames
}

// col returns a field's values as strings, by field name.
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
	names := make([]string, len(f.Fields))
	for i, fld := range f.Fields {
		names[i] = fld.Name
	}
	t.Fatalf("no field %q in %v", name, names)
	return nil
}

func field(t *testing.T, f *data.Frame, name string) *data.Field {
	t.Helper()
	for _, fld := range f.Fields {
		if fld.Name == name {
			return fld
		}
	}
	t.Fatalf("no field %q", name)
	return nil
}

func TestMeasureHistory(t *testing.T) {
	ds, f := serverDS(t)
	fr := frames(t, ds, `{"queryType":"measureHistory","projects":["demo:shop-api"],"metrics":["coverage","bugs","alert_status"]}`)
	if len(fr) != 3 {
		t.Fatalf("%d frames, want 3", len(fr))
	}
	// Frames follow the order the metrics were chosen in.
	cov, bugs, gate := fr[0], fr[1], fr[2]
	if got := strings.Join(col(t, cov, "coverage"), " "); got != "34.1 47 52.9 66.7 75 80.5" {
		t.Errorf("coverage = %s", got)
	}
	if cf := field(t, cov, "coverage"); cf.Config.Unit != "percent" || cf.Config.DisplayNameFromDS != "Coverage" || cf.Labels["project"] != "Shop API" {
		t.Errorf("coverage config = %+v labels = %v", cf.Config, cf.Labels)
	}
	if cf := field(t, bugs, "bugs"); cf.Type() != data.FieldTypeNullableFloat64 || cf.Config.Decimals == nil || *cf.Config.Decimals != 0 {
		t.Errorf("bugs field type %v config %+v", cf.Type(), cf.Config)
	}
	// The quality gate is text, with readable names.
	if got := strings.Join(col(t, gate, "alert_status"), " "); got != "OK OK ERROR OK OK OK" {
		t.Errorf("alert_status = %s", got)
	}
	if gf := field(t, gate, "alert_status"); gf.Type() != data.FieldTypeNullableString || len(gf.Config.Mappings) != 1 {
		t.Errorf("alert_status type %v mappings %v", gf.Type(), gf.Config.Mappings)
	}
	if first := col(t, cov, "time")[0]; !strings.HasPrefix(first, "2026-08-29 06:13:39") {
		t.Errorf("first time = %s", first)
	}
	if cov.Meta.Type != data.FrameTypeTimeSeriesMulti {
		t.Errorf("frame type = %v", cov.Meta.Type)
	}

	// The dashboard time range is passed on.
	q := f.Calls("api/measures/search_history")[0].Query
	if q.Get("from") != "2026-08-01T00:00:00+0000" || q.Get("to") != "2026-11-21T00:00:00+0000" {
		t.Errorf("from=%q to=%q", q.Get("from"), q.Get("to"))
	}
}

func TestMeasureHistoryNamesSeriesByWhatVaries(t *testing.T) {
	ds, _ := serverDS(t)
	name := func(query string) []string {
		var out []string
		for _, fr := range frames(t, ds, query) {
			out = append(out, fr.Fields[1].Config.DisplayNameFromDS)
		}
		return out
	}
	if got := name(`{"projects":["demo:shop-api,demo:shop-web"],"metrics":["coverage"]}`); strings.Join(got, "|") != "Shop API|Shop Web" {
		t.Errorf("several projects, one metric: %v", got)
	}
	if got := name(`{"projects":["demo:shop-api","demo:shop-web"],"metrics":["coverage","bugs"]}`); strings.Join(got, "|") != "Shop API · Coverage|Shop API · Bugs|Shop Web · Coverage|Shop Web · Bugs" {
		t.Errorf("several of both: %v", got)
	}
	// A project that is not in the list is shown by its key.
	if got := name(`{"projects":["other:key"],"metrics":["coverage","bugs"]}`); strings.Join(got, "|") != "Coverage|Bugs" {
		t.Errorf("one project: %v", got)
	}
}

func TestMeasures(t *testing.T) {
	ds, f := serverDS(t)
	fr := frames(t, ds, `{"queryType":"measures","metrics":["alert_status","coverage","bugs","sqale_rating","new_coverage"]}`)[0]
	if got := strings.Join(col(t, fr, "project"), "|"); got != "Legacy Billing|Shop API|Shop Web" {
		t.Errorf("projects = %s", got)
	}
	if got := strings.Join(col(t, fr, "alert_status"), "|"); got != "ERROR|OK|ERROR" {
		t.Errorf("gate = %s", got)
	}
	if got := strings.Join(col(t, fr, "bugs"), "|"); got != "9|1|4" {
		t.Errorf("bugs = %s", got)
	}
	// A metric no project has a value for is an empty column, not an error.
	if got := strings.Join(col(t, fr, "new_coverage"), "|"); got != "null|null|null" {
		t.Errorf("new_coverage = %s", got)
	}
	rating := field(t, fr, "sqale_rating")
	if rating.Config.DisplayName != "Maintainability Rating" || len(rating.Config.Mappings) != 1 {
		t.Errorf("rating config = %+v", rating.Config)
	}
	if link := field(t, fr, "project").Config.Links[0].URL; link != f.URL+"/dashboard?id=${__data.fields.key:percentencode}" {
		t.Errorf("project link = %s", link)
	}
	if last := col(t, fr, "last_analysis")[1]; !strings.HasPrefix(last, "2026-11-18") {
		t.Errorf("last analysis = %s", last)
	}
	// Every listed project was asked for in one request.
	calls := f.Calls("api/measures/search")
	if len(calls) != 1 || calls[0].Query.Get("projectKeys") != "demo:legacy-billing,demo:shop-api,demo:shop-web" {
		t.Errorf("measure requests: %+v", calls)
	}
}

func TestIssues(t *testing.T) {
	ds, f := serverDS(t)
	r := run(t, ds, `{"queryType":"issues","projects":["demo:shop-api"],"resolution":"all","createdInRange":true,"limit":2}`)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	fr := r.Frames[0]
	if n, _ := fr.RowLen(); n != 2 {
		t.Fatalf("%d rows", n)
	}
	if got := col(t, fr, "status")[0]; got != "CLOSED (FIXED)" {
		t.Errorf("status = %s", got)
	}
	if got := col(t, fr, "file")[0]; got != "src/checks.py" {
		t.Errorf("file = %s", got)
	}
	if got := col(t, fr, "project")[0]; got != "Shop API" {
		t.Errorf("project = %s", got)
	}
	link := col(t, fr, "link")[0]
	if !strings.HasPrefix(link, f.URL+"/project/issues?id=demo%3Ashop-api&issues=") || !strings.Contains(link, "&open=") {
		t.Errorf("link = %s", link)
	}
	// 15 match but only 2 were asked for: the panel says so.
	if len(fr.Meta.Notices) != 1 || !strings.Contains(fr.Meta.Notices[0].Text, "newest 2 of 15") {
		t.Errorf("notices = %+v", fr.Meta.Notices)
	}
	q := f.Calls("api/issues/search")[0].Query
	if q.Has("resolved") || q.Get("createdAfter") != "2026-08-01T00:00:00+0000" || q.Get("createdBefore") != "2026-11-21T00:00:00+0000" {
		t.Errorf("issue query = %v", q)
	}

	// Open issues, any age: the default.
	f.Reset()
	frames(t, ds, `{"queryType":"issues"}`)
	q = f.Calls("api/issues/search")[0].Query
	if q.Get("resolved") != "false" || q.Has("createdAfter") || q.Has("componentKeys") || q.Get("ps") != "100" {
		t.Errorf("default issue query = %v", q)
	}
}

func TestIssueCounts(t *testing.T) {
	ds, _ := serverDS(t)
	fr := frames(t, ds, `{"queryType":"issueCounts","groupBy":"severities"}`)[0]
	// Severities are ordered worst first, whatever their counts.
	if got := strings.Join(col(t, fr, "severity"), "|"); got != "BLOCKER|CRITICAL|MAJOR|MINOR|INFO" {
		t.Errorf("severities = %s", got)
	}
	if got := strings.Join(col(t, fr, "issues"), "|"); got != "1|4|10|0|0" {
		t.Errorf("counts = %s", got)
	}
	fr = frames(t, ds, `{"queryType":"issueCounts","groupBy":"types"}`)[0]
	// Other groupings are ordered by count.
	if got := strings.Join(col(t, fr, "type"), "|"); got != "BUG|VULNERABILITY|CODE_SMELL" {
		t.Errorf("types = %s", got)
	}
}

func TestAnalyses(t *testing.T) {
	ds, _ := serverDS(t)
	fr := frames(t, ds, `{"queryType":"analyses","projects":["demo:shop-api"],"categories":["VERSION","QUALITY_GATE"]}`)[0]
	text := col(t, fr, "text")
	if len(text) != 6 || text[0] != "Version 2.2" || text[2] != "Quality gate: Passed; Version 2.0" || text[3] != "Quality gate: Failed (New Issues > 0); Version 1.2" {
		t.Errorf("events = %q", text)
	}
	// The server-upgrade event of the first analysis is filtered out.
	if text[5] != "Version 1.0" || col(t, fr, "tags")[5] != "VERSION" {
		t.Errorf("first analysis: text=%q tags=%q", text[5], col(t, fr, "tags")[5])
	}
	// Only analyses with a wanted event remain.
	fr = frames(t, ds, `{"queryType":"analyses","projects":["demo:shop-api"],"categories":["QUALITY_GATE"]}`)[0]
	if got := strings.Join(col(t, fr, "text"), "|"); got != "Quality gate: Passed|Quality gate: Failed (New Issues > 0)" {
		t.Errorf("quality gate events = %s", got)
	}
	// Without categories every analysis is returned, newest first.
	fr = frames(t, ds, `{"queryType":"analyses","projects":["demo:shop-api"]}`)[0]
	times := col(t, fr, "time")
	if len(times) != 6 || times[0] < times[5] {
		t.Errorf("times = %v", times)
	}
	// Annotations pick up fields by these names.
	for _, name := range []string{"time", "text", "tags"} {
		field(t, fr, name)
	}
}

func TestListQueries(t *testing.T) {
	ds, _ := serverDS(t)
	fr := frames(t, ds, `{"queryType":"projects"}`)[0]
	if got := strings.Join(col(t, fr, "name"), "|") + " / " + strings.Join(col(t, fr, "key"), "|"); got != "Legacy Billing|Shop API|Shop Web / demo:legacy-billing|demo:shop-api|demo:shop-web" {
		t.Errorf("projects = %s", got)
	}
	if got := col(t, fr, "tags")[0]; got != "backend, legacy" {
		t.Errorf("tags = %s", got)
	}
	fr = frames(t, ds, `{"queryType":"metrics"}`)[0]
	names, keys := col(t, fr, "name"), col(t, fr, "key")
	// The fixture has 16 metrics; the hidden one is left out.
	if len(names) != 15 || names[0] != "Bugs" || keys[0] != "bugs" || strings.Contains(strings.Join(keys, " "), "last_commit_date") {
		t.Errorf("metrics = %v / %v", names, keys)
	}
	fr = frames(t, ds, `{"queryType":"branches","projects":["demo:shop-api"]}`)[0]
	if got := col(t, fr, "name")[0] + " " + col(t, fr, "main")[0] + " " + col(t, fr, "quality_gate")[0]; got != "main true OK" {
		t.Errorf("branches = %s", got)
	}
}

func TestQueryErrors(t *testing.T) {
	ds, f := serverDS(t)
	cases := []struct{ name, query, want string }{
		{"validation", `{"queryType":"measureHistory","metrics":["coverage"]}`, "Choose at least one project."},
		{"unknown project", `{"projects":["nope"],"metrics":["coverage"]}`, "SonarQube: Component key 'nope' not found. Check the key"},
		{"unresolved variable", `{"projects":["$project"],"metrics":["coverage"]}`, `The project "$project" looks like a dashboard variable`},
	}
	for _, c := range cases {
		r := run(t, ds, c.query)
		if r.Error == nil || !strings.Contains(r.Error.Error(), c.want) || r.ErrorSource != backend.ErrorSourceDownstream || r.Status != backend.StatusBadRequest {
			t.Errorf("%s: error=%v status=%v source=%v", c.name, r.Error, r.Status, r.ErrorSource)
		}
	}
	// A rejected query makes no request.
	f.Reset()
	run(t, ds, `{"projects":["a&b=c"],"metrics":["coverage"]}`)
	if len(f.Calls("api/measures/search_history")) != 0 {
		t.Error("an invalid query reached SonarQube")
	}

	// Wrong token: a message that says what to do, with the right status.
	bad := newDS(t, f, `{}`, "squ_wrong")
	r := run(t, bad, `{"projects":["demo:shop-api"],"metrics":["coverage"]}`)
	if r.Error == nil || !strings.Contains(r.Error.Error(), "rejected the token") || r.Status != backend.StatusUnauthorized {
		t.Errorf("wrong token: %v (%v)", r.Error, r.Status)
	}
	if strings.Contains(r.Error.Error(), "squ_wrong") {
		t.Error("the error message contains the token")
	}
	none := newDS(t, f, `{}`, "")
	r = run(t, none, `{"projects":["demo:shop-api"],"metrics":["coverage"]}`)
	if r.Error == nil || !strings.Contains(r.Error.Error(), "requires authentication") {
		t.Errorf("no token: %v", r.Error)
	}
}

func TestSlowServerTimesOut(t *testing.T) {
	ds, f := serverDS(t)
	ds.settings.Timeout = 100 * time.Millisecond
	f.Handlers["api/measures/search_history"] = func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	start := time.Now()
	r := run(t, ds, `{"projects":["demo:shop-api"],"metrics":["coverage"]}`)
	if r.Error == nil || !strings.Contains(r.Error.Error(), "did not answer within 0 seconds") || r.Status != backend.StatusTimeout {
		t.Errorf("timeout: %v (%v)", r.Error, r.Status)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestSettingsErrorIsReportedEverywhere(t *testing.T) {
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{URL: "https://sonarcloud.io", JSONData: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	ds := inst.(*Datasource)
	want := "Configuration problem: SonarQube Cloud needs an organization key"
	if r := run(t, ds, `{"queryType":"projects"}`); r.Error == nil || !strings.Contains(r.Error.Error(), want) {
		t.Errorf("query: %v", r.Error)
	}
	h, _ := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, want) {
		t.Errorf("health: %+v", h)
	}
}

func TestCheckHealth(t *testing.T) {
	health := func(ds *Datasource) *backend.CheckHealthResult {
		h, err := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	f := sonartest.New(t)
	f.Token = testToken

	h := health(newDS(t, f, `{}`, testToken))
	if h.Status != backend.HealthStatusOk || h.Message != "Connected to SonarQube Server 26.11.0.134175. The token can see 3 projects." {
		t.Errorf("ok: %+v", h)
	}
	h = health(newDS(t, f, `{}`, "squ_wrong"))
	if h.Status != backend.HealthStatusError || !strings.HasPrefix(h.Message, "SonarQube rejected the token.") {
		t.Errorf("wrong token: %+v", h)
	}
	h = health(newDS(t, f, `{}`, ""))
	if h.Status != backend.HealthStatusError || !strings.HasPrefix(h.Message, "This SonarQube requires authentication.") {
		t.Errorf("no token: %+v", h)
	}

	// Not SonarQube at all.
	f.Handlers["api/system/status"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>hello</html>")) }
	h = health(newDS(t, f, `{}`, testToken))
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "did not answer like SonarQube") || !strings.Contains(h.Message, "/sonarqube") {
		t.Errorf("not sonarqube: %+v", h)
	}
	// Something that wants its own login before SonarQube's open status check.
	f.Handlers["api/system/status"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	h = health(newDS(t, f, `{}`, testToken))
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "asked for a login before answering SonarQube's status check") {
		t.Errorf("login wall: %+v", h)
	}
	// Still starting.
	f.Handlers["api/system/status"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"STARTING"}`)) }
	h = health(newDS(t, f, `{}`, testToken))
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "not ready (status STARTING)") {
		t.Errorf("starting: %+v", h)
	}
	delete(f.Handlers, "api/system/status")

	// A token that sees nothing is a success with advice.
	f.Handlers["api/components/search_projects"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paging":{"pageIndex":1,"pageSize":500,"total":0},"components":[]}`))
	}
	h = health(newDS(t, f, `{}`, testToken))
	if h.Status != backend.HealthStatusOk || !strings.Contains(h.Message, "can see 0 projects. Give the token's user the Browse permission") {
		t.Errorf("no projects: %+v", h)
	}

	// Unreachable.
	inst, _ := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{URL: "http://127.0.0.1:1", JSONData: json.RawMessage(`{}`)})
	h = health(inst.(*Datasource))
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "Cannot reach SonarQube at http://127.0.0.1:1") || !strings.Contains(h.Message, "Private data source connect") {
		t.Errorf("unreachable: %+v", h)
	}
}

// SonarQube Cloud is recognised by its host name, which a fake cannot have;
// mark the instance as Cloud after the factory built it.
func cloudDS(t *testing.T, f *sonartest.Fake, organization, token string) *Datasource {
	t.Helper()
	f.Cloud = true
	ds := newDS(t, f, `{"organization":"`+organization+`"}`, token)
	ds.settings.Cloud = true
	return ds
}

func TestCloud(t *testing.T) {
	f := sonartest.New(t)
	ds := cloudDS(t, f, "sonarsource", "")

	h, _ := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusOk || h.Message != "Connected to SonarQube Cloud, organization sonarsource. Anonymous access can see 83 projects. Only public projects are visible without a token." {
		t.Errorf("health: %+v", h)
	}

	fr := frames(t, ds, `{"queryType":"measureHistory","projects":["SonarSource_echoes-react"],"metrics":["coverage"]}`)[0]
	if got := col(t, fr, "coverage"); len(got) != 5 || got[0] != "96.8" {
		t.Errorf("cloud coverage = %v", got)
	}
	fr = frames(t, ds, `{"queryType":"issues","projects":["SonarSource_echoes-react"],"resolution":"all","limit":1}`)[0]
	if got := col(t, fr, "status")[0]; got != "RESOLVED (FALSE-POSITIVE)" {
		t.Errorf("cloud issue status = %s", got)
	}
	// Issue searches without projects are scoped to the organization.
	f.Reset()
	frames(t, ds, `{"queryType":"issueCounts"}`)
	if got := f.Calls("api/issues/search")[0].Query.Get("organization"); got != "sonarsource" {
		t.Errorf("organization = %q", got)
	}

	// A mistyped organization is explained with Cloud's own message.
	typo := cloudDS(t, f, "typo", "")
	h, _ = typo.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "SonarQube Cloud: No organization for key 'typo'") {
		t.Errorf("wrong organization: %+v", h)
	}
}

func TestResources(t *testing.T) {
	ds, f := serverDS(t)
	call := func(path string) (int, string) {
		var status int
		var body string
		err := ds.CallResource(context.Background(), &backend.CallResourceRequest{Method: http.MethodGet, Path: strings.SplitN(path, "?", 2)[0], URL: path},
			backend.CallResourceResponseSenderFunc(func(r *backend.CallResourceResponse) error {
				status, body = r.Status, string(r.Body)
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		return status, body
	}

	status, body := call("projects")
	if status != 200 || !strings.Contains(body, `{"key":"demo:shop-api","name":"Shop API"}`) || !strings.Contains(body, `"total":3`) {
		t.Errorf("projects: %d %s", status, body)
	}
	status, body = call("metrics")
	if status != 200 || !strings.Contains(body, `"key":"coverage"`) {
		t.Errorf("metrics: %d %s", status, body)
	}
	status, body = call("branches?project=demo:shop-api")
	if status != 200 || strings.TrimSpace(body) != `[{"name":"main","isMain":true}]` {
		t.Errorf("branches: %d %s", status, body)
	}
	status, body = call("branches?project=a%26b")
	if status != 400 || !strings.Contains(body, "is not a valid project") {
		t.Errorf("bad project: %d %s", status, body)
	}
	if status, _ := call("nothing"); status != 404 {
		t.Errorf("unknown resource: %d", status)
	}

	// The lists are cached: the dropdowns and the queries share one fetch.
	f.Reset()
	call("projects")
	call("metrics")
	frames(t, ds, `{"queryType":"measures","metrics":["coverage"]}`)
	if n := len(f.Calls("api/components/search_projects")) + len(f.Calls("api/metrics/search")); n != 0 {
		t.Errorf("%d list requests after the cache was warm", n)
	}

	// An authentication failure reaches the editor with its status.
	bad := newDS(t, f, `{}`, "squ_wrong")
	var st int
	_ = bad.CallResource(context.Background(), &backend.CallResourceRequest{Method: http.MethodGet, Path: "projects", URL: "projects"},
		backend.CallResourceResponseSenderFunc(func(r *backend.CallResourceResponse) error { st = r.Status; return nil }))
	if st != http.StatusUnauthorized {
		t.Errorf("wrong token on a resource: %d", st)
	}
}

func TestQueriesRunConcurrentlyButBounded(t *testing.T) {
	ds, f := serverDS(t)
	var inFlight, maxSeen atomic.Int32
	f.Handlers["api/measures/search_history"] = func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(sonartest.Fixture("server_history")))
	}
	projects := make([]string, 30)
	for i := range projects {
		projects[i] = fmt.Sprintf("p%d", i)
	}
	js, _ := json.Marshal(map[string]any{"projects": projects, "metrics": []string{"coverage"}})
	if r := run(t, ds, string(js)); r.Error != nil {
		t.Fatal(r.Error)
	}
	if got := maxSeen.Load(); got < 2 || got > maxParallelProjects {
		t.Errorf("up to %d requests at once, want 2..%d", got, maxParallelProjects)
	}
}

func TestColumnTypes(t *testing.T) {
	ds, f := serverDS(t)
	f.Handlers["api/measures/search"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"measures":[
			{"metric":"sqale_index","value":"125","component":"demo:shop-api"},
			{"metric":"last_commit_date","value":"1700000000000","component":"demo:shop-api"},
			{"metric":"ncloc_language_distribution","value":"py=120;js=30","component":"demo:shop-api"},
			{"metric":"plugin_metric","value":"7.5","component":"demo:shop-api"},
			{"metric":"plugin_text","value":"hello","component":"demo:shop-api"},
			{"metric":"new_coverage","periods":[{"index":1,"value":"88.2"}],"component":"demo:shop-api"}]}`))
	}
	fr := frames(t, ds, `{"queryType":"measures","projects":["demo:shop-api"],"metrics":["sqale_index","last_commit_date","ncloc_language_distribution","plugin_metric","plugin_text","new_coverage","coverage"]}`)[0]
	want := map[string]struct {
		typ   data.FieldType
		value string
		unit  string
	}{
		"sqale_index": {data.FieldTypeNullableFloat64, "125", "m"},
		// Hidden on the server, so not in the catalogue: a plain number.
		"last_commit_date":            {data.FieldTypeNullableFloat64, "1.7e+12", ""},
		"ncloc_language_distribution": {data.FieldTypeNullableString, "py=120;js=30", ""},
		// Not in the server's catalogue: a number when it parses as one.
		"plugin_metric": {data.FieldTypeNullableFloat64, "7.5", ""},
		"plugin_text":   {data.FieldTypeNullableString, "hello", ""},
		"new_coverage":  {data.FieldTypeNullableFloat64, "88.2", "percent"},
		"coverage":      {data.FieldTypeNullableFloat64, "null", "percent"},
	}
	for name, w := range want {
		fld := field(t, fr, name)
		if fld.Type() != w.typ || col(t, fr, name)[0] != w.value || fld.Config.Unit != w.unit {
			t.Errorf("%s: type=%v value=%s unit=%q, want %v %s %q", name, fld.Type(), col(t, fr, name)[0], fld.Config.Unit, w.typ, w.value, w.unit)
		}
	}
}

// A new panel shows the last 6 hours, which rarely holds an analysis: the
// empty result says so and when the latest analysis was.
func TestEmptyTimeRangeIsExplained(t *testing.T) {
	ds, f := serverDS(t)
	f.Handlers["api/measures/search_history"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paging":{"pageIndex":1,"pageSize":1000,"total":0},"measures":[{"metric":"coverage","history":[]}]}`))
	}
	fr := frames(t, ds, `{"projects":["demo:shop-api","demo:legacy-billing"],"metrics":["coverage"]}`)
	if len(fr) != 2 || len(fr[0].Meta.Notices) != 1 {
		t.Fatalf("%d frames, notices %+v", len(fr), fr[0].Meta.Notices)
	}
	want := "No analysis in the selected time range. The latest analysis was on 18 November 2026: choose a time range that includes it, such as the last 90 days."
	if got := fr[0].Meta.Notices[0].Text; got != want {
		t.Errorf("notice = %q", got)
	}
	// Only the first frame carries it.
	if fr[1].Meta.Notices != nil {
		t.Errorf("second frame has notices: %+v", fr[1].Meta.Notices)
	}

	f.Handlers["api/project_analyses/search"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paging":{"pageIndex":1,"pageSize":500,"total":0},"analyses":[]}`))
	}
	an := frames(t, ds, `{"queryType":"analyses","projects":["demo:shop-web"]}`)[0]
	if len(an.Meta.Notices) != 1 || !strings.Contains(an.Meta.Notices[0].Text, "latest analysis was on 19 November 2026") {
		t.Errorf("analyses notices = %+v", an.Meta.Notices)
	}
	// Analyses exist but none has the chosen event: a different explanation.
	delete(f.Handlers, "api/project_analyses/search")
	an = frames(t, ds, `{"queryType":"analyses","projects":["demo:shop-api"],"categories":["QUALITY_PROFILE"]}`)[0]
	if len(an.Meta.Notices) != 1 || !strings.HasPrefix(an.Meta.Notices[0].Text, "None of the 6 analyses in the selected time range has one of the chosen events.") {
		t.Errorf("filtered-out notices = %+v", an.Meta.Notices)
	}
	// A project that is not in the list gives the general advice.
	fr = frames(t, ds, `{"projects":["other:key"],"metrics":["coverage"]}`)
	if got := fr[0].Meta.Notices[0].Text; !strings.Contains(got, "try a wider time range") {
		t.Errorf("notice = %q", got)
	}
	// A result with data has no notice.
	delete(f.Handlers, "api/measures/search_history")
	if fr = frames(t, ds, `{"projects":["demo:shop-api"],"metrics":["coverage"]}`); fr[0].Meta.Notices != nil {
		t.Errorf("unexpected notice: %+v", fr[0].Meta.Notices)
	}
}
