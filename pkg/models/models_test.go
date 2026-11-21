package models

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func inst(url, jsonData string, secure map[string]string) backend.DataSourceInstanceSettings {
	return backend.DataSourceInstanceSettings{URL: url, JSONData: json.RawMessage(jsonData), DecryptedSecureJSONData: secure}
}

func TestLoadSettings(t *testing.T) {
	s, err := LoadSettings(inst(" https://sonarqube.example.com/sonar/ ", `{}`, map[string]string{"token": " squ_abc \n"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://sonarqube.example.com/sonar" || s.Cloud || s.Token != "squ_abc" || s.Timeout != 30*time.Second || s.Organization != "" {
		t.Errorf("settings = %+v", s)
	}

	s, err = LoadSettings(inst("https://sonarcloud.io", `{"organization":" my-org ","timeoutSeconds":120}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Cloud || s.Organization != "my-org" || s.Token != "" || s.Timeout != 2*time.Minute {
		t.Errorf("cloud settings = %+v", s)
	}
}

// People paste the address of a page or of the API; the base is kept.
func TestLoadSettingsNormalisesURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://sq:9000":                         "http://sq:9000",
		"http://sq:9000/":                        "http://sq:9000",
		"http://sq:9000/api":                     "http://sq:9000",
		"http://sq:9000/api/":                    "http://sq:9000",
		"https://tools.example.com/sonarqube/":   "https://tools.example.com/sonarqube",
		"https://tools.example.com/sonar/api":    "https://tools.example.com/sonar",
		"https://sq.example.com/projects":        "https://sq.example.com",
		"https://sq.example.com/sessions/new":    "https://sq.example.com",
		"HTTPS://SQ.example.com":                 "https://SQ.example.com",
		"https://sq.example.com:8443/sonarqube/": "https://sq.example.com:8443/sonarqube",
	} {
		s, err := LoadSettings(inst(in, `{}`, nil))
		if err != nil || s.URL != want {
			t.Errorf("%q -> %q, %v; want %q", in, urlOf(s), err, want)
		}
	}
}

func urlOf(s *Settings) string {
	if s == nil {
		return ""
	}
	return s.URL
}

func TestCloudDetection(t *testing.T) {
	for url, cloud := range map[string]bool{
		"https://sonarcloud.io":           true,
		"https://sonarcloud.io/":          true,
		"https://www.sonarcloud.io":       true,
		"https://SonarCloud.io":           true,
		"https://sonarqube.us":            true,
		"https://sonarcloud.io.evil.test": false,
		"https://sonarqube.example.com":   false,
		"https://notsonarcloud.io":        false,
	} {
		s, err := LoadSettings(inst(url, `{"organization":"o"}`, nil))
		if err != nil || s.Cloud != cloud {
			t.Errorf("%s: cloud=%v err=%v, want %v", url, s != nil && s.Cloud, err, cloud)
		}
	}
}

func TestLoadSettingsInvalid(t *testing.T) {
	cases := []struct {
		name, url, json, token, want string
	}{
		{"no url", "", `{}`, "", "URL is required"},
		{"no scheme", "sonarqube.example.com", `{}`, "", "must start with http:// or https://"},
		{"other scheme", "ftp://sq", `{}`, "", "must start with http:// or https://"},
		{"no host", "https://", `{}`, "", "has no host"},
		{"credentials in url", "https://admin:secret@sq.example.com", `{}`, "", "not in the URL"},
		{"query string", "https://sq.example.com/?a=1", `{}`, "", "query string or fragment"},
		{"cloud without organization", "https://sonarcloud.io", `{}`, "", "needs an organization key"},
		{"cloud over http", "http://sonarcloud.io", `{"organization":"o"}`, "", "only served over https"},
		{"bad organization", "https://sonarcloud.io", `{"organization":"my org/x"}`, "", "organization key may only contain"},
		{"token with spaces", "https://sq.example.com", `{}`, "squ abc", "contains whitespace"},
		{"timeout too long", "https://sq.example.com", `{"timeoutSeconds":301}`, "", "between 1 and 300"},
		{"negative timeout", "https://sq.example.com", `{"timeoutSeconds":-1}`, "", "between 1 and 300"},
		{"broken json", "https://sq.example.com", `{`, "", "could not parse"},
	}
	for _, c := range cases {
		_, err := LoadSettings(inst(c.url, c.json, map[string]string{"token": c.token}))
		if err == nil || !errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an invalid-settings error containing %q", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error repeats a credential: %v", c.name, err)
		}
	}
}

func parse(t *testing.T, js string) (*QueryModel, error) {
	t.Helper()
	return ParseQuery(json.RawMessage(js))
}

func TestParseQueryDefaults(t *testing.T) {
	q, err := parse(t, `{"projects":["demo:shop-api"],"metrics":["coverage"]}`)
	if err != nil || q.QueryType != QueryMeasureHistory {
		t.Fatalf("%+v, %v", q, err)
	}
	q, err = parse(t, `{"queryType":"issues"}`)
	if err != nil || q.Resolution != ResolutionOpen || q.Limit != DefaultIssueLimit {
		t.Fatalf("%+v, %v", q, err)
	}
	q, err = parse(t, `{"queryType":"issueCounts"}`)
	if err != nil || q.GroupBy != "severities" {
		t.Fatalf("%+v, %v", q, err)
	}
	for _, typ := range []string{"projects", "metrics"} {
		if _, err := parse(t, `{"queryType":"`+typ+`"}`); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}

// A multi-value dashboard variable arrives as one comma-separated entry.
func TestParseQueryLists(t *testing.T) {
	q, err := parse(t, `{"projects":["a:one, b.two","a:one"," ","c-3"],"metrics":["coverage,bugs","bugs"],
		"queryType":"issues","severities":["blocker,Critical"],"types":["bug"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(q.Projects, "|") != "a:one|b.two|c-3" || strings.Join(q.Metrics, "|") != "coverage|bugs" {
		t.Errorf("projects=%v metrics=%v", q.Projects, q.Metrics)
	}
	if strings.Join(q.Severities, "|") != "BLOCKER|CRITICAL" || strings.Join(q.Types, "|") != "BUG" {
		t.Errorf("severities=%v types=%v", q.Severities, q.Types)
	}
}

func TestParseQueryInvalid(t *testing.T) {
	projects := make([]string, MaxProjects+1)
	for i := range projects {
		projects[i] = "p" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
	}
	tooMany, _ := json.Marshal(map[string]any{"projects": projects, "metrics": []string{"coverage"}})

	cases := []struct{ name, js, want string }{
		{"not json", `[`, "could not read the query"},
		{"unknown type", `{"queryType":"drop"}`, `unknown query type "drop"`},
		{"history without project", `{"metrics":["coverage"]}`, "choose at least one project"},
		{"history without metric", `{"projects":["p"]}`, "choose at least one metric"},
		{"measures without metric", `{"queryType":"measures"}`, "choose at least one metric"},
		{"analyses without project", `{"queryType":"analyses"}`, "choose at least one project"},
		{"branches without project", `{"queryType":"branches"}`, "choose at least one project"},
		{"unresolved variable", `{"projects":["$project"],"metrics":["coverage"]}`, "looks like a dashboard variable"},
		{"unresolved braces variable", `{"projects":["p"],"metrics":["${metric}"]}`, "looks like a dashboard variable"},
		{"parameter injection in project", `{"projects":["a&organization=b"],"metrics":["coverage"]}`, "is not a valid project"},
		{"space in project", `{"projects":["a b"],"metrics":["coverage"]}`, "is not a valid project"},
		{"path in metric", `{"projects":["p"],"metrics":["../x"]}`, "is not a valid metric"},
		{"bad severity", `{"queryType":"issues","severities":["HUGE"]}`, "is not a valid severity"},
		{"bad type", `{"queryType":"issues","types":["TYPO"]}`, "is not a valid issue type"},
		{"bad category", `{"queryType":"analyses","projects":["p"],"categories":["PARTY"]}`, "is not a valid event category"},
		{"bad resolution", `{"queryType":"issues","resolution":"maybe"}`, "unknown issue status filter"},
		{"bad group", `{"queryType":"issueCounts","groupBy":"moon"}`, `cannot be grouped by "moon"`},
		{"limit too high", `{"queryType":"issues","limit":10001}`, "between 1 and 10000"},
		{"negative limit", `{"queryType":"issues","limit":-5}`, "between 1 and 10000"},
		{"branch with comma", `{"projects":["p"],"metrics":["coverage"],"branch":"a,b"}`, "not a valid branch name"},
		{"branch with newline", `{"projects":["p"],"metrics":["coverage"],"branch":"a\nb"}`, "not a valid branch name"},
		{"too many projects", string(tooMany), "too many projects"},
	}
	for _, c := range cases {
		_, err := parse(t, c.js)
		if err == nil || !errors.Is(err, ErrInvalidQuery) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an invalid-query error containing %q", c.name, err, c.want)
		}
	}
}

func TestParseQueryAcceptsRealBranchNames(t *testing.T) {
	for _, b := range []string{"main", "release/1.2", "feature/JIRA-123_fix", "dépendances", "v1.0.0"} {
		js, _ := json.Marshal(map[string]any{"projects": []string{"p"}, "metrics": []string{"coverage"}, "branch": b})
		if _, err := ParseQuery(js); err != nil {
			t.Errorf("branch %q: %v", b, err)
		}
	}
}

// Long invalid input must not be echoed in full: messages are logged.
func TestInvalidValuesAreTruncatedInMessages(t *testing.T) {
	js, _ := json.Marshal(map[string]any{"projects": []string{strings.Repeat("?", 5000)}, "metrics": []string{"coverage"}})
	_, err := ParseQuery(js)
	if err == nil || len(err.Error()) > 200 {
		t.Errorf("message is %d bytes", len(err.Error()))
	}
}
