package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/models"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func refOf(qm *models.QueryModel) sonar.Ref {
	return sonar.Ref{Branch: qm.Branch, PullRequest: qm.PullRequest}
}

// refParams is the part of a SonarQube page address that selects a branch or
// a pull request.
func refParams(qm *models.QueryModel) string {
	switch {
	case qm.PullRequest != "":
		return "&pullRequest=" + url.QueryEscape(qm.PullRequest)
	case qm.Branch != "":
		return "&branch=" + url.QueryEscape(qm.Branch)
	}
	return ""
}

// gateDetails is the value of the quality_gate_details measure: the gate's
// conditions as SonarQube evaluated them in the latest analysis.
type gateDetails struct {
	Level      string `json:"level"`
	Conditions []struct {
		Metric string `json:"metric"`
		// Op is the comparison that fails the condition: LT, GT, EQ or NE.
		Op     string `json:"op"`
		Error  string `json:"error"`
		Actual string `json:"actual"`
		Level  string `json:"level"`
	} `json:"conditions"`
}

var ratingLetters = map[string]string{"1": "A", "2": "B", "3": "C", "4": "D", "5": "E"}

// formatValue writes a measure's raw value the way SonarQube shows it.
func formatValue(m sonar.Metric, raw string) string {
	if raw == "" {
		return ""
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	switch m.Type {
	case "RATING":
		if l := ratingLetters[strconv.Itoa(int(n))]; l != "" {
			return l
		}
	case "PERCENT":
		return strconv.FormatFloat(n, 'f', -1, 64) + "%"
	case "WORK_DUR":
		return strconv.FormatFloat(n, 'f', -1, 64) + " min"
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// requirement says what a condition asks for. op is the comparison that
// fails it, so the requirement is its opposite.
func requirement(m sonar.Metric, op, threshold string) string {
	v := formatValue(m, threshold)
	switch op {
	case "LT":
		return "at least " + v
	case "GT":
		if m.Type == "RATING" {
			return v + " or better"
		}
		return "at most " + v
	case "EQ":
		return "not " + v
	case "NE":
		return v
	}
	return v
}

var conditionOrder = map[string]int{"ERROR": 0, "WARN": 1, "OK": 2}

// qualityGate returns one row per quality gate condition of each project:
// what it measures, the value, what the gate requires, and whether it
// passed. It answers "why did the gate fail".
func (d *Datasource) qualityGate(ctx context.Context, qm *models.QueryModel) (data.Frames, error) {
	idx, err := d.metricIndex(ctx)
	if err != nil {
		return nil, err
	}
	keys, notice, err := d.resolveProjects(ctx, qm)
	if err != nil {
		return nil, err
	}
	var measures []sonar.Measure
	if len(keys) > 0 {
		if measures, err = d.client.Measures(ctx, keys, []string{"quality_gate_details"}, refOf(qm)); err != nil {
			return nil, err
		}
	}
	details := map[string]gateDetails{}
	for _, m := range measures {
		var g gateDetails
		if v := m.Val(); v != nil && json.Unmarshal([]byte(*v), &g) == nil {
			details[m.Component] = g
		}
	}
	names := d.projectNames(ctx)

	var project, pkey, gate, condition, status, value, required, metric []string
	evaluated, conditions := 0, 0
	for _, key := range keys {
		g, ok := details[key]
		if !ok {
			continue
		}
		evaluated++
		conds := append(g.Conditions[:0:0], g.Conditions...)
		sort.SliceStable(conds, func(a, b int) bool { return conditionOrder[conds[a].Level] < conditionOrder[conds[b].Level] })
		for _, c := range conds {
			conditions++
			if qm.FailedOnly && c.Level != "ERROR" {
				continue
			}
			m := metricOf(idx, c.Metric)
			project, pkey, gate = append(project, nameOf(names, key)), append(pkey, key), append(gate, g.Level)
			condition, status = append(condition, m.Name), append(status, c.Level)
			value, required = append(value, formatValue(m, c.Actual)), append(required, requirement(m, c.Op, c.Error))
			metric = append(metric, c.Metric)
		}
	}

	projectField := named(data.NewField("project", nil, orEmpty(project)), "Project")
	projectField.Config.Links = []data.DataLink{{Title: "Open in SonarQube", URL: d.settings.URL + "/dashboard?id=${__data.fields.key:percentencode}" + refParams(qm), TargetBlank: true}}
	gateField := named(data.NewField("quality_gate", nil, orEmpty(gate)), "Quality gate")
	gateField.Config.Mappings = levelMapping
	statusField := named(data.NewField("status", nil, orEmpty(status)), "Status")
	statusField.Config.Mappings = levelMapping
	frame := data.NewFrame("quality gate",
		// What a reader wants first: which condition, did it pass, the value
		// and what was required. The project's overall gate comes after.
		projectField,
		named(data.NewField("condition", nil, orEmpty(condition)), "Condition"),
		statusField,
		named(data.NewField("value", nil, orEmpty(value)), "Value"),
		named(data.NewField("required", nil, orEmpty(required)), "Required"),
		gateField,
		named(data.NewField("metric", nil, orEmpty(metric)), "Metric key"),
		named(data.NewField("key", nil, orEmpty(pkey)), "Key"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	addNotice(frame, notice)
	switch {
	case len(project) > 0:
	case evaluated == 0 && len(keys) > 0:
		addNotice(frame, "SonarQube has no quality gate result for the selected projects. A project gets one with its first analysis.")
	case qm.FailedOnly && conditions > 0:
		addNotice(frame, fmt.Sprintf("No condition failed: all %d quality gate conditions of the selected projects passed.", conditions))
	}
	return data.Frames{frame}, nil
}

var probabilityMapping = data.ValueMappings{data.ValueMapper{
	"HIGH":   {Text: "High", Color: "red"},
	"MEDIUM": {Text: "Medium", Color: "orange"},
	"LOW":    {Text: "Low", Color: "yellow"},
}}

var hotspotStatusMapping = data.ValueMappings{data.ValueMapper{
	"TO_REVIEW":    {Text: "To review"},
	"REVIEWED":     {Text: "Reviewed"},
	"FIXED":        {Text: "Fixed", Color: "green"},
	"SAFE":         {Text: "Safe", Color: "green"},
	"ACKNOWLEDGED": {Text: "Acknowledged", Color: "orange"},
}}

var probabilityOrder = map[string]int{"HIGH": 0, "MEDIUM": 1, "LOW": 2}

// Names of SonarQube's security categories whose slug does not read well.
var categoryNames = map[string]string{
	"auth": "Authentication", "dos": "Denial of service", "csrf": "Cross-site request forgery",
	"xss": "Cross-site scripting", "rce": "Code injection", "ssrf": "Server-side request forgery",
	"sql-injection": "SQL injection", "xxe": "XML external entity", "ldap-injection": "LDAP injection",
	"xpath-injection": "XPath injection",
	"insecure-conf":   "Insecure configuration",
}

func categoryName(slug string) string {
	if n := categoryNames[slug]; n != "" {
		return n
	}
	words := strings.ReplaceAll(slug, "-", " ")
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

type hotspotPage struct {
	list  []sonar.Hotspot
	total int
}

// hotspots returns a table of security hotspots of the selected projects,
// highest review priority first.
func (d *Datasource) hotspots(ctx context.Context, qm *models.QueryModel) (data.Frames, error) {
	status := ""
	switch qm.HotspotStatus {
	case models.HotspotsToReview:
		status = "TO_REVIEW"
	case models.HotspotsReviewed:
		status = "REVIEWED"
	}
	pages, err := forEach(ctx, qm.Projects, func(ctx context.Context, project string) (hotspotPage, error) {
		list, total, err := d.client.Hotspots(ctx, project, refOf(qm), status, qm.Limit)
		return hotspotPage{list, total}, err
	})
	if err != nil {
		return nil, err
	}
	var (
		list  []sonar.Hotspot
		total int
	)
	for i, p := range pages {
		total += p.total
		for _, h := range p.list {
			if h.Project == "" {
				h.Project = qm.Projects[i]
			}
			list = append(list, h)
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		pa, pb := probabilityOrder[list[a].VulnerabilityProbability], probabilityOrder[list[b].VulnerabilityProbability]
		if pa != pb {
			return pa < pb
		}
		return list[a].CreationDate > list[b].CreationDate
	})
	if len(list) > qm.Limit {
		list = list[:qm.Limit]
	}
	names := d.projectNames(ctx)

	n := len(list)
	var (
		created     = make([]*time.Time, n)
		updated     = make([]*time.Time, n)
		project     = make([]string, n)
		probability = make([]string, n)
		state       = make([]string, n)
		resolution  = make([]string, n)
		category    = make([]string, n)
		message     = make([]string, n)
		rule        = make([]string, n)
		file        = make([]string, n)
		line        = make([]*int64, n)
		assignee    = make([]string, n)
		author      = make([]string, n)
		key         = make([]string, n)
		link        = make([]string, n)
	)
	for i, h := range list {
		if t, ok := sonar.ParseTime(h.CreationDate); ok {
			created[i] = &t
		}
		if t, ok := sonar.ParseTime(h.UpdateDate); ok {
			updated[i] = &t
		}
		project[i], probability[i], state[i], resolution[i] = nameOf(names, h.Project), h.VulnerabilityProbability, h.Status, h.Resolution
		category[i], message[i], rule[i] = categoryName(h.SecurityCategory), h.Message, h.RuleKey
		file[i], line[i], assignee[i], author[i], key[i] = h.File(), h.Line, h.Assignee, h.Author, h.Key
		link[i] = d.settings.URL + "/security_hotspots?id=" + url.QueryEscape(h.Project) + "&hotspots=" + url.QueryEscape(h.Key) + refParams(qm)
	}

	prob := named(data.NewField("priority", nil, probability), "Review priority")
	prob.Config.Mappings = probabilityMapping
	st := named(data.NewField("status", nil, state), "Status")
	st.Config.Mappings = hotspotStatusMapping
	res := named(data.NewField("resolution", nil, resolution), "Resolution")
	res.Config.Mappings = hotspotStatusMapping
	msg := named(data.NewField("message", nil, message), "Message")
	msg.Config.Links = []data.DataLink{{Title: "Open in SonarQube", URL: "${__data.fields.link}", TargetBlank: true}}

	frame := data.NewFrame("hotspots",
		named(data.NewField("created", nil, created), "Created"),
		named(data.NewField("project", nil, project), "Project"),
		prob,
		named(data.NewField("category", nil, category), "Category"),
		st, res, msg,
		named(data.NewField("rule", nil, rule), "Rule"),
		named(data.NewField("file", nil, file), "File"),
		named(data.NewField("line", nil, line), "Line"),
		named(data.NewField("assignee", nil, assignee), "Assignee"),
		named(data.NewField("author", nil, author), "Author"),
		named(data.NewField("updated", nil, updated), "Updated"),
		named(data.NewField("key", nil, key), "Key"),
		named(data.NewField("link", nil, link), "Link"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	switch {
	case total > n:
		addNotice(frame, fmt.Sprintf("Showing %d of %d matching hotspots, highest review priority first. Raise the limit (up to %d) to see others.", n, total, models.MaxIssueLimit))
	case n == 0:
		addNotice(frame, "No matching security hotspots. Some SonarQube versions have no hotspot rules and report these findings as vulnerabilities, which the Issues query lists.")
	}
	return data.Frames{frame}, nil
}

// pullRequests returns the pull requests SonarQube keeps for the selected
// projects, most recently analysed first.
func (d *Datasource) pullRequests(ctx context.Context, qm *models.QueryModel) (data.Frames, error) {
	lists, err := forEach(ctx, qm.Projects, func(ctx context.Context, project string) ([]sonar.PullRequest, error) {
		return d.client.PullRequests(ctx, project)
	})
	if err != nil {
		return nil, err
	}
	names := d.projectNames(ctx)

	type row struct {
		pr      sonar.PullRequest
		project string
	}
	var rows []row
	for i, project := range qm.Projects {
		for _, pr := range lists[i] {
			rows = append(rows, row{pr, project})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].pr.AnalysisDate > rows[b].pr.AnalysisDate })

	n := len(rows)
	var (
		key, title, project, branch, target = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		gate, external, pkey, link          = make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		bugs, vulns, smells                 = make([]*int64, n), make([]*int64, n), make([]*int64, n)
		last                                = make([]*time.Time, n)
	)
	for i, r := range rows {
		pr := r.pr
		key[i], title[i], project[i], branch[i] = pr.Key, pr.Title, nameOf(names, r.project), pr.Branch
		target[i] = pr.Target
		if target[i] == "" {
			target[i] = pr.Base
		}
		gate[i], external[i], pkey[i] = pr.Status.QualityGateStatus, pr.URL, r.project
		bugs[i], vulns[i], smells[i] = pr.Status.Bugs, pr.Status.Vulnerabilities, pr.Status.CodeSmells
		if t, ok := sonar.ParseTime(pr.AnalysisDate); ok {
			last[i] = &t
		}
		link[i] = d.settings.URL + "/dashboard?id=" + url.QueryEscape(r.project) + "&pullRequest=" + url.QueryEscape(pr.Key)
	}
	titleField := named(data.NewField("title", nil, title), "Pull request")
	titleField.Config.Links = []data.DataLink{{Title: "Open in SonarQube", URL: "${__data.fields.link}", TargetBlank: true}}
	gateField := named(data.NewField("quality_gate", nil, gate), "Quality gate")
	gateField.Config.Mappings = levelMapping
	frame := data.NewFrame("pull requests",
		named(data.NewField("key", nil, key), "Number"),
		titleField,
		named(data.NewField("project", nil, project), "Project"),
		named(data.NewField("branch", nil, branch), "Branch"),
		named(data.NewField("target", nil, target), "Target"),
		gateField,
		named(data.NewField("bugs", nil, bugs), "Bugs"),
		named(data.NewField("vulnerabilities", nil, vulns), "Vulnerabilities"),
		named(data.NewField("code_smells", nil, smells), "Code smells"),
		named(data.NewField("last_analysis", nil, last), "Last analysis"),
		named(data.NewField("url", nil, external), "Pull request URL"),
		named(data.NewField("project_key", nil, pkey), "Project key"),
		named(data.NewField("link", nil, link), "Link"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	if n == 0 {
		addNotice(frame, "SonarQube has no pull requests for the selected projects. It lists a pull request once it has been analysed, and deletes it some time after its last analysis.")
	}
	return data.Frames{frame}, nil
}
