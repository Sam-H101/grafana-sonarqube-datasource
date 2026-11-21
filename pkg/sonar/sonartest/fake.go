// Package sonartest provides a fake SonarQube for unit tests. It answers the
// API endpoints the plugin uses with responses captured from a real SonarQube
// Server (Community Build 26.11) and from SonarQube Cloud; see
// docs/sonarqube-api-notes.md for how they were taken.
package sonartest

import (
	"embed"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

// Fixture returns a captured response, for example "server_history".
func Fixture(name string) string {
	b, err := fixtures.ReadFile("testdata/" + name + ".json")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Call is one request the fake received.
type Call struct {
	Path   string
	Query  url.Values
	Header http.Header
}

// Fake is a SonarQube stand-in. Set its fields before the first request.
type Fake struct {
	URL string
	// Cloud answers like SonarQube Cloud: its fixtures, anonymous access
	// allowed, and "organization" required to list projects.
	Cloud bool
	// Token, when set, is the only token accepted. Requests without it get
	// 401, except api/system/status and api/authentication/validate.
	Token string
	// Handlers override the answer for a path such as
	// "api/measures/search_history".
	Handlers map[string]http.HandlerFunc

	mu    sync.Mutex
	calls []Call
}

// New starts a fake that is closed when the test ends.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{Handlers: map[string]http.HandlerFunc{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

// Calls returns the requests received for a path, oldest first.
func (f *Fake) Calls(path string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// Reset forgets the recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *Fake) prefix() string {
	if f.Cloud {
		return "cloud_"
	}
	return "server_"
}

// Error writes an API error the way SonarQube does.
func Error(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"errors":[{"msg":%q}]}`, msg)
}

func (f *Fake) authorised(r *http.Request) bool {
	if f.Token == "" {
		return true
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(f.Token+":"))
	return r.Header.Get("Authorization") == want
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	f.mu.Lock()
	f.calls = append(f.calls, Call{Path: path, Query: r.URL.Query(), Header: r.Header.Clone()})
	f.mu.Unlock()

	if h, ok := f.Handlers[path]; ok {
		h(w, r)
		return
	}
	json := func(body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	q := r.URL.Query()

	switch path {
	case "api/system/status":
		json(Fixture(f.prefix() + "status"))
		return
	case "api/authentication/validate":
		valid := f.authorised(r)
		if r.Header.Get("Authorization") == "" {
			// Anonymous: allowed on Cloud, not on a server that forces
			// authentication.
			valid = f.Cloud
		}
		json(fmt.Sprintf(`{"valid":%t}`, valid))
		return
	}

	if r.Header.Get("Authorization") == "" && !f.Cloud || !f.authorised(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch path {
	case "api/components/search_projects":
		if f.Cloud && q.Get("organization") != "sonarsource" {
			Error(w, http.StatusNotFound, fmt.Sprintf("No organization for key '%s'", q.Get("organization")))
			return
		}
		json(Fixture(f.prefix() + "search_projects"))
	case "api/metrics/search":
		json(Fixture("server_metrics"))
	case "api/measures/search_history":
		if q.Get("component") == "nope" {
			Error(w, http.StatusNotFound, "Component key 'nope' not found")
			return
		}
		json(Fixture(f.prefix() + "history"))
	case "api/measures/search":
		json(Fixture(f.prefix() + "measures"))
	case "api/measures/component":
		json(fmt.Sprintf(`{"component":{"key":%q,"measures":[{"metric":"coverage","value":"61.5"},{"metric":"new_coverage","period":{"index":1,"value":"12.0"}}]}}`, q.Get("component")))
	case "api/issues/search":
		if q.Get("components") != "" {
			// SonarQube Cloud rejects the newer parameter name.
			Error(w, http.StatusBadRequest, "At least one of the following parameters must be specified: organization, projects, projectKeys (deprecated), componentKeys, assignees, issues")
			return
		}
		json(Fixture(f.prefix() + "issues"))
	case "api/project_analyses/search":
		json(Fixture(f.prefix() + "analyses"))
	case "api/project_branches/list":
		json(Fixture(f.prefix() + "branches"))
	default:
		http.NotFound(w, r)
	}
}
