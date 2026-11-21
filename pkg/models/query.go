package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// QueryType selects what a query returns.
type QueryType string

const (
	// QueryMeasureHistory returns metric values over time, one series per
	// project and metric.
	QueryMeasureHistory QueryType = "measureHistory"
	// QueryMeasures returns the current metric values, one row per project.
	QueryMeasures QueryType = "measures"
	// QueryIssues returns a list of issues.
	QueryIssues QueryType = "issues"
	// QueryIssueCounts returns issue counts grouped by one property.
	QueryIssueCounts QueryType = "issueCounts"
	// QueryAnalyses returns analyses and their events (new versions, quality
	// gate changes), for tables and annotations.
	QueryAnalyses QueryType = "analyses"
	// QueryQualityGate returns the conditions of each project's quality gate
	// with their thresholds and actual values.
	QueryQualityGate QueryType = "qualityGate"
	// QueryHotspots returns a list of security hotspots.
	QueryHotspots QueryType = "hotspots"
	// QueryPullRequests returns the pull requests of the selected projects.
	QueryPullRequests QueryType = "pullRequests"
	// QueryProjects returns the list of projects.
	QueryProjects QueryType = "projects"
	// QueryMetrics returns the metrics the server knows (for variables).
	QueryMetrics QueryType = "metrics"
	// QueryBranches returns the branches of the selected projects (for
	// variables).
	QueryBranches QueryType = "branches"
)

// Limits on what one query may ask for. They bound the number of requests a
// single panel can cause.
const (
	MaxProjects       = 200
	MaxMetrics        = 30
	DefaultIssueLimit = 100
	// MaxIssueLimit is SonarQube's own cap: it returns only the first 10,000
	// results of a search.
	MaxIssueLimit = 10000
)

// Resolution filters issues by whether they are still open.
type Resolution string

const (
	ResolutionOpen     Resolution = "open"
	ResolutionResolved Resolution = "resolved"
	ResolutionAll      Resolution = "all"
)

// HotspotStatus filters security hotspots by review status.
type HotspotStatus string

const (
	HotspotsToReview HotspotStatus = "toReview"
	HotspotsReviewed HotspotStatus = "reviewed"
	HotspotsAll      HotspotStatus = "all"
)

// QueryModel is one query as saved in a dashboard. Mirrors src/types.ts.
type QueryModel struct {
	QueryType QueryType `json:"queryType"`
	// Projects are project keys. Empty means every project the token can see,
	// for the query types that allow it.
	Projects []string `json:"projects"`
	Metrics  []string `json:"metrics"`
	Branch   string   `json:"branch"`
	// PullRequest is a pull request's key (usually its number). A query is
	// about the main branch, a branch or a pull request, never two of them.
	PullRequest string `json:"pullRequest"`

	// Issue filters.
	Severities     []string   `json:"severities"`
	Types          []string   `json:"types"`
	Resolution     Resolution `json:"resolution"`
	CreatedInRange bool       `json:"createdInRange"`
	Limit          int        `json:"limit"`
	// GroupBy is the property issue counts are grouped by.
	GroupBy string `json:"groupBy"`

	// Categories filters analysis events (VERSION, QUALITY_GATE, ...).
	Categories []string `json:"categories"`

	// Tags narrows the list of projects to those with one of these tags.
	Tags []string `json:"tags"`
	// HotspotStatus filters security hotspots.
	HotspotStatus HotspotStatus `json:"hotspotStatus"`
	// FailedOnly keeps only the quality gate conditions that failed.
	FailedOnly bool `json:"failedOnly"`
}

// ErrInvalidQuery wraps every query validation failure.
var ErrInvalidQuery = errors.New("invalid query")

func badQuery(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, fmt.Sprintf(format, a...))
}

var (
	// Project keys: letters, digits, '-', '_', '.' and ':' (SonarQube's own
	// rule), up to 400 characters.
	projectKeyPattern = regexp.MustCompile(`^[\p{L}\p{N}._:\-]{1,400}$`)
	metricKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	// Branch names are free-form in Git; exclude only what cannot be one.
	branchPattern = regexp.MustCompile(`^[^\s,\x00-\x1f\x7f]{1,255}$`)
	// Pull request keys are numbers on most platforms; allow the characters
	// an identifier may have.
	pullRequestPattern = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,100}$`)
	// Project tags: SonarQube allows lower-case letters, digits and + - # .
	tagPattern = regexp.MustCompile(`^[a-z0-9+\-#.]{1,100}$`)

	severities = set("INFO", "MINOR", "MAJOR", "CRITICAL", "BLOCKER")
	issueTypes = set("BUG", "VULNERABILITY", "CODE_SMELL")
	categories = set("VERSION", "QUALITY_GATE", "QUALITY_PROFILE", "DEFINITION_CHANGE", "ISSUE_DETECTION", "SQ_UPGRADE", "OTHER")
	// GroupBys are the issue facets, by their API names, that both SonarQube
	// Server and SonarQube Cloud accept without a project filter. ("files"
	// needs a project on Server and does not exist on Cloud.)
	GroupBys = []string{"severities", "types", "projects", "rules", "tags", "languages", "assignees", "author", "resolutions", "statuses"}
)

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// ParseQuery reads and validates a query. Unset fields get defaults.
func ParseQuery(raw json.RawMessage) (*QueryModel, error) {
	var q QueryModel
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, badQuery("could not read the query")
	}
	if q.QueryType == "" {
		q.QueryType = QueryMeasureHistory
	}

	var err error
	if q.Projects, err = cleanList(q.Projects, "project", MaxProjects, func(s string) bool { return projectKeyPattern.MatchString(s) }, false); err != nil {
		return nil, err
	}
	if q.Metrics, err = cleanList(q.Metrics, "metric", MaxMetrics, func(s string) bool { return metricKeyPattern.MatchString(s) }, false); err != nil {
		return nil, err
	}
	if q.Severities, err = cleanList(q.Severities, "severity", 10, func(s string) bool { return severities[s] }, true); err != nil {
		return nil, err
	}
	if q.Types, err = cleanList(q.Types, "issue type", 10, func(s string) bool { return issueTypes[s] }, true); err != nil {
		return nil, err
	}
	if q.Categories, err = cleanList(q.Categories, "event category", 10, func(s string) bool { return categories[s] }, true); err != nil {
		return nil, err
	}
	for i, tag := range q.Tags {
		q.Tags[i] = strings.ToLower(tag)
	}
	if q.Tags, err = cleanList(q.Tags, "tag", 50, func(s string) bool { return tagPattern.MatchString(s) }, false); err != nil {
		return nil, err
	}
	q.Branch = strings.TrimSpace(q.Branch)
	if q.Branch != "" && !branchPattern.MatchString(q.Branch) {
		return nil, badQuery("%q is not a valid branch name", q.Branch)
	}
	q.PullRequest = strings.TrimPrefix(strings.TrimSpace(q.PullRequest), "#")
	if strings.HasPrefix(q.PullRequest, "$") {
		return nil, badQuery("the pull request %q looks like a dashboard variable that has no value here", q.PullRequest)
	}
	if q.PullRequest != "" && !pullRequestPattern.MatchString(q.PullRequest) {
		return nil, badQuery("%q is not a valid pull request: enter its number", truncate(q.PullRequest, 60))
	}
	if q.Branch != "" && q.PullRequest != "" {
		return nil, badQuery("choose a branch or a pull request, not both")
	}

	switch q.QueryType {
	case QueryMeasureHistory:
		if len(q.Projects) == 0 {
			return nil, badQuery("choose at least one project")
		}
		if len(q.Metrics) == 0 {
			return nil, badQuery("choose at least one metric")
		}
	case QueryMeasures:
		if len(q.Metrics) == 0 {
			return nil, badQuery("choose at least one metric")
		}
	case QueryIssues, QueryIssueCounts:
		switch q.Resolution {
		case "":
			q.Resolution = ResolutionOpen
		case ResolutionOpen, ResolutionResolved, ResolutionAll:
		default:
			return nil, badQuery("unknown issue status filter %q", q.Resolution)
		}
		if q.QueryType == QueryIssueCounts {
			if q.GroupBy == "" {
				q.GroupBy = "severities"
			}
			if !contains(GroupBys, q.GroupBy) {
				return nil, badQuery("issues cannot be grouped by %q", q.GroupBy)
			}
		}
		switch {
		case q.Limit == 0:
			q.Limit = DefaultIssueLimit
		case q.Limit < 0 || q.Limit > MaxIssueLimit:
			return nil, badQuery("the issue limit must be between 1 and %d (SonarQube returns at most the first %d issues of a search)", MaxIssueLimit, MaxIssueLimit)
		}
	case QueryHotspots:
		// SonarQube lists hotspots one project at a time.
		if len(q.Projects) == 0 {
			return nil, badQuery("choose at least one project")
		}
		switch q.HotspotStatus {
		case "":
			q.HotspotStatus = HotspotsToReview
		case HotspotsToReview, HotspotsReviewed, HotspotsAll:
		default:
			return nil, badQuery("unknown hotspot status filter %q", q.HotspotStatus)
		}
		switch {
		case q.Limit == 0:
			q.Limit = DefaultIssueLimit
		case q.Limit < 0 || q.Limit > MaxIssueLimit:
			return nil, badQuery("the limit must be between 1 and %d", MaxIssueLimit)
		}
	case QueryAnalyses, QueryBranches, QueryPullRequests:
		if len(q.Projects) == 0 {
			return nil, badQuery("choose at least one project")
		}
	case QueryQualityGate:
	case QueryProjects, QueryMetrics:
	default:
		return nil, badQuery("unknown query type %q", q.QueryType)
	}
	return &q, nil
}

// cleanList trims, drops empties and duplicates, and validates each entry.
// Entries may themselves be comma-separated: that is how a multi-value
// dashboard variable arrives.
func cleanList(in []string, what string, limit int, valid func(string) bool, upper bool) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, entry := range in {
		for _, v := range strings.Split(entry, ",") {
			v = strings.TrimSpace(v)
			if upper {
				v = strings.ToUpper(v)
			}
			if v == "" || seen[v] {
				continue
			}
			if strings.HasPrefix(v, "$") {
				return nil, badQuery("the %s %q looks like a dashboard variable that has no value here", what, v)
			}
			if !valid(v) {
				if what == "project" || what == "metric" {
					// Usually a name typed where a key is needed.
					return nil, badQuery("%q is not a valid %s key: choose a %s from the list, or enter its key", truncate(v, 60), what, what)
				}
				return nil, badQuery("%q is not a valid %s", truncate(v, 60), what)
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > limit {
		return nil, badQuery("too many %ss: %d selected, the limit is %d", what, len(out), limit)
	}
	return out, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
