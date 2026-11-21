// Package sonar is a client for the SonarQube Web API, as served by SonarQube
// Server and SonarQube Cloud. It only reads. docs/sonarqube-api-notes.md
// records the observed behaviour the client relies on.
package sonar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// maxResponseBytes caps one response body.
	maxResponseBytes = 32 << 20
	// TimeLayout is how the API writes and reads datetimes.
	TimeLayout = "2006-01-02T15:04:05-0700"
	userAgent  = "grafana-sonarqube-datasource"
)

var (
	// ErrUnauthorized is a 401: the token is missing, wrong, expired or revoked.
	ErrUnauthorized = errors.New("sonarqube: unauthorized")
	// ErrForbidden is a 403: the token is valid but lacks permission.
	ErrForbidden = errors.New("sonarqube: forbidden")
	// ErrNotSonarQube means the server answered, but not like the SonarQube
	// API: usually a wrong URL, a missing context path, or a login page.
	ErrNotSonarQube = errors.New("sonarqube: unexpected response")
	// ErrResponseTooLarge means a response exceeded maxResponseBytes.
	ErrResponseTooLarge = errors.New("sonarqube: response too large")
)

// APIError is an error reported by the API with a message ({"errors":[...]}).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// NotFound reports whether the API said a project, branch or other thing
// does not exist.
func (e *APIError) NotFound() bool { return e.Status == http.StatusNotFound }

// Config is what the client needs to reach one SonarQube.
type Config struct {
	// BaseURL has no trailing slash and may include a context path.
	BaseURL string
	// Token is sent as the user name of HTTP Basic authentication with an
	// empty password. Every SonarQube Server version and SonarQube Cloud
	// accept that form; Bearer is only accepted from Server 10.0.
	Token string
	// Organization is added to the requests that need it on SonarQube Cloud.
	Organization string
	Cloud        bool
}

// Client talks to one SonarQube. It is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
	auth string
}

// NewClient returns a client that sends its requests through hc.
func NewClient(cfg Config, hc *http.Client) (*Client, error) {
	if _, err := url.Parse(cfg.BaseURL); err != nil || cfg.BaseURL == "" {
		return nil, errors.New("invalid base URL")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	c := &Client{cfg: cfg, http: hc}
	if cfg.Token != "" {
		c.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Token+":"))
	}
	return c, nil
}

// HasToken reports whether requests are authenticated.
func (c *Client) HasToken() bool { return c.auth != "" }

type apiErrors struct {
	Errors []struct {
		Msg string `json:"msg"`
	} `json:"errors"`
}

// get calls one API endpoint and decodes its JSON answer into out.
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.cfg.BaseURL + "/" + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// Drain a little so the connection can be reused, then close.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return ErrResponseTooLarge
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	}
	if resp.StatusCode >= 400 {
		var ae apiErrors
		if json.Unmarshal(body, &ae) == nil && len(ae.Errors) > 0 {
			msgs := make([]string, 0, len(ae.Errors))
			for _, e := range ae.Errors {
				msgs = append(msgs, e.Msg)
			}
			return &APIError{Status: resp.StatusCode, Message: cleanMessage(strings.Join(msgs, "; "))}
		}
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: HTTP 404 for %s", ErrNotSonarQube, path)
		}
		return &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("SonarQube answered HTTP %d", resp.StatusCode)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: the answer to %s is not JSON", ErrNotSonarQube, path)
	}
	return nil
}

// cleanMessage keeps an API message to one bounded line.
func cleanMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}

// Status is the answer of api/system/status, which needs no authentication.
type Status struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}

// Status reports the server version and whether it is up.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.get(ctx, "api/system/status", nil, &s); err != nil {
		return nil, err
	}
	if s.Status == "" {
		return nil, fmt.Errorf("%w: api/system/status has no status", ErrNotSonarQube)
	}
	return &s, nil
}

// ValidToken reports whether the configured token is accepted. Without a
// token the answer says whether anonymous access is allowed.
func (c *Client) ValidToken(ctx context.Context) (bool, error) {
	var v struct {
		Valid bool `json:"valid"`
	}
	if err := c.get(ctx, "api/authentication/validate", nil, &v); err != nil {
		return false, err
	}
	return v.Valid, nil
}

// Project is one project (a "component" of qualifier TRK).
type Project struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	AnalysisDate string   `json:"analysisDate"`
	Tags         []string `json:"tags"`
	Visibility   string   `json:"visibility"`
	Organization string   `json:"organization"`
}

type paging struct {
	PageIndex int `json:"pageIndex"`
	PageSize  int `json:"pageSize"`
	Total     int `json:"total"`
}

// Projects lists up to limit projects, sorted by name, and the total number
// the token can see.
func (c *Client) Projects(ctx context.Context, limit int) ([]Project, int, error) {
	const pageSize = 500
	var (
		all   []Project
		total int
	)
	for page := 1; len(all) < limit; page++ {
		params := url.Values{"ps": {strconv.Itoa(pageSize)}, "p": {strconv.Itoa(page)}, "f": {"analysisDate"}, "s": {"name"}}
		c.addOrganization(params)
		var out struct {
			Paging     paging    `json:"paging"`
			Components []Project `json:"components"`
		}
		if err := c.get(ctx, "api/components/search_projects", params, &out); err != nil {
			return nil, 0, err
		}
		total = out.Paging.Total
		all = append(all, out.Components...)
		if len(out.Components) < pageSize || len(all) >= total {
			break
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

func (c *Client) addOrganization(params url.Values) {
	if c.cfg.Organization != "" {
		params.Set("organization", c.cfg.Organization)
	}
}

// Metric describes one metric the server can measure.
type Metric struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Domain      string `json:"domain"`
	// Type is INT, FLOAT, PERCENT, BOOL, STRING, MILLISEC, DATA, LEVEL,
	// DISTRIB, RATING or WORK_DUR.
	Type   string `json:"type"`
	Hidden bool   `json:"hidden"`
}

// Metrics lists every metric that is not hidden.
func (c *Client) Metrics(ctx context.Context) ([]Metric, error) {
	const pageSize = 500
	var all []Metric
	for page := 1; page <= 10; page++ {
		var out struct {
			Metrics []Metric `json:"metrics"`
			Total   int      `json:"total"`
		}
		params := url.Values{"ps": {strconv.Itoa(pageSize)}, "p": {strconv.Itoa(page)}}
		if err := c.get(ctx, "api/metrics/search", params, &out); err != nil {
			return nil, err
		}
		for _, m := range out.Metrics {
			if !m.Hidden {
				all = append(all, m)
			}
		}
		if len(out.Metrics) < pageSize {
			break
		}
	}
	return all, nil
}

// Point is one value of a metric at the time of an analysis. Value is the
// API's string form; it is absent when the metric had no value then.
type Point struct {
	Date  string  `json:"date"`
	Value *string `json:"value"`
}

// History is the values of one metric over time, oldest first.
type History struct {
	Metric string  `json:"metric"`
	Points []Point `json:"history"`
}

// maxHistoryPages bounds one history request to 20,000 analyses.
const maxHistoryPages = 20

// MeasureHistory returns the history of metrics for one project between from
// and to (zero times mean unbounded).
func (c *Client) MeasureHistory(ctx context.Context, project string, metrics []string, branch string, from, to time.Time) ([]History, error) {
	const pageSize = 1000
	byMetric := map[string]*History{}
	var order []string
	for page := 1; page <= maxHistoryPages; page++ {
		params := url.Values{
			"component": {project},
			"metrics":   {strings.Join(metrics, ",")},
			"ps":        {strconv.Itoa(pageSize)},
			"p":         {strconv.Itoa(page)},
		}
		if branch != "" {
			params.Set("branch", branch)
		}
		if !from.IsZero() {
			params.Set("from", from.UTC().Format(TimeLayout))
		}
		if !to.IsZero() {
			params.Set("to", to.UTC().Format(TimeLayout))
		}
		var out struct {
			Paging   paging    `json:"paging"`
			Measures []History `json:"measures"`
		}
		if err := c.get(ctx, "api/measures/search_history", params, &out); err != nil {
			return nil, err
		}
		for i := range out.Measures {
			m := out.Measures[i]
			h, ok := byMetric[m.Metric]
			if !ok {
				h = &History{Metric: m.Metric}
				byMetric[m.Metric] = h
				order = append(order, m.Metric)
			}
			h.Points = append(h.Points, m.Points...)
		}
		if page*pageSize >= out.Paging.Total {
			break
		}
	}
	result := make([]History, 0, len(order))
	for _, k := range order {
		result = append(result, *byMetric[k])
	}
	return result, nil
}

// Measure is the current value of one metric for one project.
type Measure struct {
	Metric    string  `json:"metric"`
	Component string  `json:"component"`
	Value     *string `json:"value"`
	// "New code" metrics carry their value in a period.
	Period  *period  `json:"period"`
	Periods []period `json:"periods"`
}

type period struct {
	Value *string `json:"value"`
}

// Val returns the measure's value, wherever the API put it.
func (m Measure) Val() *string {
	switch {
	case m.Value != nil:
		return m.Value
	case m.Period != nil && m.Period.Value != nil:
		return m.Period.Value
	case len(m.Periods) > 0:
		return m.Periods[0].Value
	}
	return nil
}

const (
	measuresMaxProjects = 100
	measuresMaxMetrics  = 15
)

// Measures returns the current values of metrics for projects. Without a
// branch it reads the main branch of many projects per request; with a
// branch it asks for each project in turn.
func (c *Client) Measures(ctx context.Context, projects, metrics []string, branch string) ([]Measure, error) {
	var all []Measure
	for m := 0; m < len(metrics); m += measuresMaxMetrics {
		metricChunk := metrics[m:min(m+measuresMaxMetrics, len(metrics))]
		if branch != "" {
			for _, project := range projects {
				params := url.Values{"component": {project}, "metricKeys": {strings.Join(metricChunk, ",")}, "branch": {branch}}
				var out struct {
					Component struct {
						Key      string    `json:"key"`
						Measures []Measure `json:"measures"`
					} `json:"component"`
				}
				if err := c.get(ctx, "api/measures/component", params, &out); err != nil {
					return nil, err
				}
				for _, ms := range out.Component.Measures {
					ms.Component = project
					all = append(all, ms)
				}
			}
			continue
		}
		for p := 0; p < len(projects); p += measuresMaxProjects {
			projectChunk := projects[p:min(p+measuresMaxProjects, len(projects))]
			params := url.Values{"projectKeys": {strings.Join(projectChunk, ",")}, "metricKeys": {strings.Join(metricChunk, ",")}}
			var out struct {
				Measures []Measure `json:"measures"`
			}
			if err := c.get(ctx, "api/measures/search", params, &out); err != nil {
				return nil, err
			}
			all = append(all, out.Measures...)
		}
	}
	return all, nil
}

// Issue is one issue. Only the fields the plugin shows are read.
type Issue struct {
	Key          string   `json:"key"`
	Rule         string   `json:"rule"`
	Severity     string   `json:"severity"`
	Type         string   `json:"type"`
	Component    string   `json:"component"`
	Project      string   `json:"project"`
	Line         *int64   `json:"line"`
	Status       string   `json:"status"`
	Resolution   string   `json:"resolution"`
	Message      string   `json:"message"`
	Effort       string   `json:"effort"`
	Assignee     string   `json:"assignee"`
	Author       string   `json:"author"`
	Tags         []string `json:"tags"`
	CreationDate string   `json:"creationDate"`
	UpdateDate   string   `json:"updateDate"`
}

// File returns the issue's file path within its project.
func (i Issue) File() string {
	return strings.TrimPrefix(strings.TrimPrefix(i.Component, i.Project), ":")
}

// IssueFilter selects issues.
type IssueFilter struct {
	Projects   []string
	Branch     string
	Severities []string
	Types      []string
	// Resolved filters on resolution when non-nil.
	Resolved      *bool
	CreatedAfter  time.Time
	CreatedBefore time.Time
}

func (c *Client) issueParams(f IssueFilter) url.Values {
	params := url.Values{}
	if len(f.Projects) > 0 {
		// componentKeys is the one name accepted by every Server version and
		// by SonarQube Cloud (which rejects the newer "components").
		params.Set("componentKeys", strings.Join(f.Projects, ","))
	}
	c.addOrganization(params)
	if f.Branch != "" {
		params.Set("branch", f.Branch)
	}
	if len(f.Severities) > 0 {
		params.Set("severities", strings.Join(f.Severities, ","))
	}
	if len(f.Types) > 0 {
		params.Set("types", strings.Join(f.Types, ","))
	}
	if f.Resolved != nil {
		params.Set("resolved", strconv.FormatBool(*f.Resolved))
	}
	if !f.CreatedAfter.IsZero() {
		params.Set("createdAfter", f.CreatedAfter.UTC().Format(TimeLayout))
	}
	if !f.CreatedBefore.IsZero() {
		params.Set("createdBefore", f.CreatedBefore.UTC().Format(TimeLayout))
	}
	return params
}

// Issues returns up to limit issues, newest first, and the total number that
// match.
func (c *Client) Issues(ctx context.Context, f IssueFilter, limit int) ([]Issue, int, error) {
	pageSize := min(limit, 500)
	var (
		all   []Issue
		total int
	)
	for page := 1; len(all) < limit; page++ {
		params := c.issueParams(f)
		params.Set("s", "CREATION_DATE")
		params.Set("asc", "false")
		params.Set("ps", strconv.Itoa(pageSize))
		params.Set("p", strconv.Itoa(page))
		var out struct {
			Paging paging  `json:"paging"`
			Total  int     `json:"total"`
			Issues []Issue `json:"issues"`
		}
		if err := c.get(ctx, "api/issues/search", params, &out); err != nil {
			return nil, 0, err
		}
		total = max(out.Paging.Total, out.Total)
		all = append(all, out.Issues...)
		// The API refuses to go past the 10,000th result.
		if len(out.Issues) < pageSize || len(all) >= total || (page+1)*pageSize > 10000 {
			break
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// Count is one value of a facet and how many issues have it.
type Count struct {
	Value string `json:"val"`
	Count int64  `json:"count"`
}

// IssueCounts returns how many matching issues there are per value of facet,
// and the total number that match.
func (c *Client) IssueCounts(ctx context.Context, f IssueFilter, facet string) ([]Count, int, error) {
	params := c.issueParams(f)
	params.Set("facets", facet)
	params.Set("ps", "1")
	var out struct {
		Paging paging `json:"paging"`
		Total  int    `json:"total"`
		Facets []struct {
			Property string  `json:"property"`
			Values   []Count `json:"values"`
		} `json:"facets"`
	}
	if err := c.get(ctx, "api/issues/search", params, &out); err != nil {
		return nil, 0, err
	}
	total := max(out.Paging.Total, out.Total)
	for _, fc := range out.Facets {
		if fc.Property == facet {
			return fc.Values, total, nil
		}
	}
	return nil, total, nil
}

// Event is something notable about an analysis.
type Event struct {
	// Category is VERSION, QUALITY_GATE, QUALITY_PROFILE, OTHER, ...
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Analysis is one analysis of a project.
type Analysis struct {
	Key            string  `json:"key"`
	Date           string  `json:"date"`
	ProjectVersion string  `json:"projectVersion"`
	Revision       string  `json:"revision"`
	Events         []Event `json:"events"`
}

// maxAnalysisPages bounds one request to 5,000 analyses.
const maxAnalysisPages = 10

// Analyses returns a project's analyses between from and to, newest first.
func (c *Client) Analyses(ctx context.Context, project, branch string, from, to time.Time) ([]Analysis, error) {
	const pageSize = 500
	var all []Analysis
	for page := 1; page <= maxAnalysisPages; page++ {
		params := url.Values{"project": {project}, "ps": {strconv.Itoa(pageSize)}, "p": {strconv.Itoa(page)}}
		if branch != "" {
			params.Set("branch", branch)
		}
		if !from.IsZero() {
			params.Set("from", from.UTC().Format(TimeLayout))
		}
		if !to.IsZero() {
			params.Set("to", to.UTC().Format(TimeLayout))
		}
		var out struct {
			Paging   paging     `json:"paging"`
			Analyses []Analysis `json:"analyses"`
		}
		if err := c.get(ctx, "api/project_analyses/search", params, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Analyses...)
		if len(out.Analyses) < pageSize || len(all) >= out.Paging.Total {
			break
		}
	}
	return all, nil
}

// Branch is one branch of a project.
type Branch struct {
	Name   string `json:"name"`
	IsMain bool   `json:"isMain"`
	Type   string `json:"type"`
	Status struct {
		QualityGateStatus string `json:"qualityGateStatus"`
	} `json:"status"`
	AnalysisDate string `json:"analysisDate"`
}

// Branches lists a project's branches.
func (c *Client) Branches(ctx context.Context, project string) ([]Branch, error) {
	var out struct {
		Branches []Branch `json:"branches"`
	}
	if err := c.get(ctx, "api/project_branches/list", url.Values{"project": {project}}, &out); err != nil {
		return nil, err
	}
	return out.Branches, nil
}

// ParseTime reads an API datetime. It returns false for an empty or
// unreadable value.
func ParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
