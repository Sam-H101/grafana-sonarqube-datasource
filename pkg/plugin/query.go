package plugin

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/models"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const (
	// maxConcurrentQueries bounds how many queries of one request run at once.
	maxConcurrentQueries = 5
	// maxParallelProjects bounds the requests one query makes at once.
	maxParallelProjects = 4
)

// QueryData runs the queries of one request concurrently (bounded) and
// returns one response per RefID.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	if d.settingsErr != nil {
		p := d.explain(d.settingsErr)
		for _, q := range req.Queries {
			resp.Responses[q.RefID] = p.response()
		}
		return resp, nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxConcurrentQueries)
	)
	for _, q := range req.Queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := d.query(ctx, q)
			mu.Lock()
			resp.Responses[q.RefID] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return resp, nil
}

func (d *Datasource) query(ctx context.Context, q backend.DataQuery) backend.DataResponse {
	qm, err := models.ParseQuery(q.JSON)
	if err != nil {
		return d.explain(err).response()
	}
	ctx, cancel := context.WithTimeout(ctx, d.settings.Timeout)
	defer cancel()

	var frames data.Frames
	switch qm.QueryType {
	case models.QueryMeasureHistory:
		frames, err = d.measureHistory(ctx, qm, q.TimeRange)
	case models.QueryMeasures:
		frames, err = d.measures(ctx, qm)
	case models.QueryIssues:
		frames, err = d.issues(ctx, qm, q.TimeRange)
	case models.QueryIssueCounts:
		frames, err = d.issueCounts(ctx, qm, q.TimeRange)
	case models.QueryAnalyses:
		frames, err = d.analyses(ctx, qm, q.TimeRange)
	case models.QueryProjects:
		frames, err = d.projectList(ctx)
	case models.QueryMetrics:
		frames, err = d.metricList(ctx)
	case models.QueryBranches:
		frames, err = d.branches(ctx, qm)
	}
	if err != nil {
		p := d.explain(err)
		backend.Logger.Warn("SonarQube query failed", "refId", q.RefID, "queryType", string(qm.QueryType), "status", int(p.status), "error", logSafe(p.message))
		return p.response()
	}
	for _, f := range frames {
		f.RefID = q.RefID
	}
	return backend.DataResponse{Frames: frames}
}

// forEach runs fn for every key, a few at a time, and returns the results in
// the order of keys. The first error cancels the rest.
func forEach[T any](ctx context.Context, keys []string, fn func(ctx context.Context, key string) (T, error)) ([]T, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		out      = make([]T, len(keys))
		wg       sync.WaitGroup
		sem      = make(chan struct{}, maxParallelProjects)
		once     sync.Once
		firstErr error
	)
	for i, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			v, err := fn(ctx, key)
			if err != nil {
				once.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			out[i] = v
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// projectNames maps project keys to names. A failure to list projects is not
// an error here: keys are shown instead.
func (d *Datasource) projectNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	projects, _, err := d.projects(ctx)
	if err != nil {
		return names
	}
	for _, p := range projects {
		names[p.Key] = p.Name
	}
	return names
}

func nameOf(names map[string]string, key string) string {
	if n := names[key]; n != "" {
		return n
	}
	return key
}

// metricIndex maps metric keys to their definitions.
func (d *Datasource) metricIndex(ctx context.Context) (map[string]sonar.Metric, error) {
	metrics, err := d.metrics(ctx)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]sonar.Metric, len(metrics))
	for _, m := range metrics {
		idx[m.Key] = m
	}
	return idx, nil
}

func metricOf(idx map[string]sonar.Metric, key string) sonar.Metric {
	if m, ok := idx[key]; ok {
		return m
	}
	// Not in the catalogue (hidden, or a plugin's metric): treat as a number
	// when it parses as one.
	return sonar.Metric{Key: key, Name: key}
}

// measureHistory returns one time series per project and metric.
func (d *Datasource) measureHistory(ctx context.Context, qm *models.QueryModel, tr backend.TimeRange) (data.Frames, error) {
	idx, err := d.metricIndex(ctx)
	if err != nil {
		return nil, err
	}
	names := d.projectNames(ctx)
	histories, err := forEach(ctx, qm.Projects, func(ctx context.Context, project string) ([]sonar.History, error) {
		return d.client.MeasureHistory(ctx, project, qm.Metrics, qm.Branch, tr.From, tr.To)
	})
	if err != nil {
		return nil, err
	}

	frames := data.Frames{}
	for i, project := range qm.Projects {
		byMetric := map[string]sonar.History{}
		for _, h := range histories[i] {
			byMetric[h.Metric] = h
		}
		// Keep the order the metrics were chosen in.
		for _, key := range qm.Metrics {
			h := byMetric[key]
			m := metricOf(idx, key)
			times := make([]time.Time, 0, len(h.Points))
			col := newColumn(m, key, 0)
			for _, p := range h.Points {
				t, ok := sonar.ParseTime(p.Date)
				if !ok || p.Value == nil {
					continue
				}
				times = append(times, t)
				col.append(p.Value)
			}
			field := col.field()
			field.Labels = data.Labels{"project": nameOf(names, project)}
			switch {
			case len(qm.Projects) > 1 && len(qm.Metrics) > 1:
				field.Config.DisplayNameFromDS = nameOf(names, project) + " · " + m.Name
			case len(qm.Projects) > 1:
				field.Config.DisplayNameFromDS = nameOf(names, project)
			default:
				field.Config.DisplayNameFromDS = m.Name
			}
			frame := data.NewFrame(key, data.NewField("time", nil, times), field)
			frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesMulti, TypeVersion: data.FrameTypeVersion{0, 1}, PreferredVisualization: data.VisTypeGraph}
			frames = append(frames, frame)
		}
	}
	if points := countRows(frames); points == 0 && len(frames) > 0 {
		addNotice(frames[0], d.emptyRangeNotice(ctx, qm.Projects))
	}
	return frames, nil
}

func countRows(frames data.Frames) int {
	n := 0
	for _, f := range frames {
		rows, _ := f.RowLen()
		n += rows
	}
	return n
}

// emptyRangeNotice explains an empty result. Projects are analysed far less
// often than most data in Grafana changes, so a short time range (a new
// panel shows the last 6 hours) often holds no analysis at all.
func (d *Datasource) emptyRangeNotice(ctx context.Context, keys []string) string {
	var latest time.Time
	if projects, _, err := d.projects(ctx); err == nil {
		wanted := map[string]bool{}
		for _, k := range keys {
			wanted[k] = true
		}
		for _, p := range projects {
			if t, ok := sonar.ParseTime(p.AnalysisDate); ok && wanted[p.Key] && t.After(latest) {
				latest = t
			}
		}
	}
	if latest.IsZero() {
		return "No analysis in the selected time range. Projects are analysed less often than most data: try a wider time range, such as the last 90 days."
	}
	return fmt.Sprintf("No analysis in the selected time range. The latest analysis was on %s: choose a time range that includes it, such as the last 90 days.", latest.Format("2 January 2006"))
}

// resolveProjects returns the projects a query is about: those it names, or
// every listed project. The notice is non-empty when the list was cut short.
func (d *Datasource) resolveProjects(ctx context.Context, qm *models.QueryModel) (keys []string, notice string, err error) {
	if len(qm.Projects) > 0 {
		return qm.Projects, "", nil
	}
	projects, total, err := d.projects(ctx)
	if err != nil {
		return nil, "", err
	}
	keys = make([]string, len(projects))
	for i, p := range projects {
		keys[i] = p.Key
	}
	if total > len(projects) {
		notice = fmt.Sprintf("Showing the first %d of %d projects. Select projects to see others.", len(projects), total)
	}
	return keys, notice, nil
}

// measures returns a table with one row per project and one column per metric.
func (d *Datasource) measures(ctx context.Context, qm *models.QueryModel) (data.Frames, error) {
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
		if measures, err = d.client.Measures(ctx, keys, qm.Metrics, qm.Branch); err != nil {
			return nil, err
		}
	}
	values := map[string]map[string]*string{}
	for _, m := range measures {
		if values[m.Component] == nil {
			values[m.Component] = map[string]*string{}
		}
		values[m.Component][m.Metric] = m.Val()
	}

	names := d.projectNames(ctx)
	analysed := map[string]string{}
	if projects, _, err := d.projects(ctx); err == nil {
		for _, p := range projects {
			analysed[p.Key] = p.AnalysisDate
		}
	}

	n := len(keys)
	nameCol := make([]string, n)
	lastCol := make([]*time.Time, n)
	cols := make([]*column, len(qm.Metrics))
	for j, key := range qm.Metrics {
		cols[j] = newColumn(metricOf(idx, key), key, n)
	}
	for i, key := range keys {
		nameCol[i] = nameOf(names, key)
		if t, ok := sonar.ParseTime(analysed[key]); ok {
			lastCol[i] = &t
		}
		for j, metric := range qm.Metrics {
			cols[j].set(i, values[key][metric])
		}
	}

	project := data.NewField("project", nil, nameCol)
	project.Config = &data.FieldConfig{
		DisplayName: "Project",
		Links:       []data.DataLink{{Title: "Open in SonarQube", URL: d.settings.URL + "/dashboard?id=${__data.fields.key:percentencode}", TargetBlank: true}},
	}
	frame := data.NewFrame("measures", project, named(data.NewField("key", nil, keys), "Key"))
	for _, c := range cols {
		f := c.field()
		f.Config.DisplayName = c.metric.Name
		frame.Fields = append(frame.Fields, f)
	}
	frame.Fields = append(frame.Fields, named(data.NewField("last_analysis", nil, lastCol), "Last analysis"))
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	addNotice(frame, notice)
	return data.Frames{frame}, nil
}

func (d *Datasource) issueFilter(qm *models.QueryModel, tr backend.TimeRange) sonar.IssueFilter {
	f := sonar.IssueFilter{Projects: qm.Projects, Branch: qm.Branch, Severities: qm.Severities, Types: qm.Types}
	switch qm.Resolution {
	case models.ResolutionOpen:
		open := false
		f.Resolved = &open
	case models.ResolutionResolved:
		resolved := true
		f.Resolved = &resolved
	}
	if qm.CreatedInRange {
		f.CreatedAfter, f.CreatedBefore = tr.From, tr.To
	}
	return f
}

var severityMapping = data.ValueMappings{data.ValueMapper{
	"BLOCKER":  {Text: "Blocker", Color: "dark-red"},
	"CRITICAL": {Text: "Critical", Color: "red"},
	"MAJOR":    {Text: "Major", Color: "orange"},
	"MINOR":    {Text: "Minor", Color: "yellow"},
	"INFO":     {Text: "Info", Color: "blue"},
}}

var typeMapping = data.ValueMappings{data.ValueMapper{
	"BUG":              {Text: "Bug"},
	"VULNERABILITY":    {Text: "Vulnerability"},
	"CODE_SMELL":       {Text: "Code smell"},
	"SECURITY_HOTSPOT": {Text: "Security hotspot"},
}}

// issues returns a table of issues, newest first.
func (d *Datasource) issues(ctx context.Context, qm *models.QueryModel, tr backend.TimeRange) (data.Frames, error) {
	list, total, err := d.client.Issues(ctx, d.issueFilter(qm, tr), qm.Limit)
	if err != nil {
		return nil, err
	}
	names := d.projectNames(ctx)

	n := len(list)
	var (
		created  = make([]*time.Time, n)
		project  = make([]string, n)
		severity = make([]string, n)
		typ      = make([]string, n)
		status   = make([]string, n)
		message  = make([]string, n)
		rule     = make([]string, n)
		file     = make([]string, n)
		line     = make([]*int64, n)
		assignee = make([]string, n)
		author   = make([]string, n)
		tags     = make([]string, n)
		effort   = make([]string, n)
		updated  = make([]*time.Time, n)
		key      = make([]string, n)
		link     = make([]string, n)
	)
	for i, is := range list {
		if t, ok := sonar.ParseTime(is.CreationDate); ok {
			created[i] = &t
		}
		if t, ok := sonar.ParseTime(is.UpdateDate); ok {
			updated[i] = &t
		}
		project[i] = nameOf(names, is.Project)
		severity[i], typ[i], message[i], rule[i] = is.Severity, is.Type, is.Message, is.Rule
		status[i] = is.Status
		if is.Resolution != "" {
			status[i] = is.Status + " (" + is.Resolution + ")"
		}
		file[i], line[i] = is.File(), is.Line
		assignee[i], author[i], effort[i], key[i] = is.Assignee, is.Author, is.Effort, is.Key
		tags[i] = strings.Join(is.Tags, ", ")
		link[i] = d.settings.URL + "/project/issues?id=" + url.QueryEscape(is.Project) + "&issues=" + url.QueryEscape(is.Key) + "&open=" + url.QueryEscape(is.Key)
	}

	sev := named(data.NewField("severity", nil, severity), "Severity")
	sev.Config.Mappings = severityMapping
	ty := named(data.NewField("type", nil, typ), "Type")
	ty.Config.Mappings = typeMapping
	msg := named(data.NewField("message", nil, message), "Message")
	msg.Config.Links = []data.DataLink{{Title: "Open in SonarQube", URL: "${__data.fields.link}", TargetBlank: true}}

	frame := data.NewFrame("issues",
		named(data.NewField("created", nil, created), "Created"),
		named(data.NewField("project", nil, project), "Project"),
		sev, ty,
		named(data.NewField("status", nil, status), "Status"),
		msg,
		named(data.NewField("rule", nil, rule), "Rule"),
		named(data.NewField("file", nil, file), "File"),
		named(data.NewField("line", nil, line), "Line"),
		named(data.NewField("assignee", nil, assignee), "Assignee"),
		named(data.NewField("author", nil, author), "Author"),
		named(data.NewField("tags", nil, tags), "Tags"),
		named(data.NewField("effort", nil, effort), "Effort"),
		named(data.NewField("updated", nil, updated), "Updated"),
		named(data.NewField("key", nil, key), "Key"),
		named(data.NewField("link", nil, link), "Link"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	if total > n {
		addNotice(frame, fmt.Sprintf("Showing the newest %d of %d matching issues. Raise the limit (up to %d) or narrow the filters to see others.", n, total, models.MaxIssueLimit))
	}
	return data.Frames{frame}, nil
}

var groupNames = map[string]string{
	"severities": "Severity", "types": "Type", "projects": "Project", "rules": "Rule", "tags": "Tag",
	"languages": "Language", "assignees": "Assignee", "author": "Author",
	"resolutions": "Resolution", "statuses": "Status",
}

var severityOrder = map[string]int{"BLOCKER": 0, "CRITICAL": 1, "MAJOR": 2, "MINOR": 3, "INFO": 4}

// issueCounts returns how many issues match per value of one property.
func (d *Datasource) issueCounts(ctx context.Context, qm *models.QueryModel, tr backend.TimeRange) (data.Frames, error) {
	counts, total, err := d.client.IssueCounts(ctx, d.issueFilter(qm, tr), qm.GroupBy)
	if err != nil {
		return nil, err
	}
	if qm.GroupBy == "severities" {
		sort.SliceStable(counts, func(a, b int) bool { return severityOrder[counts[a].Value] < severityOrder[counts[b].Value] })
	} else {
		sort.SliceStable(counts, func(a, b int) bool { return counts[a].Count > counts[b].Count })
	}

	var names map[string]string
	if qm.GroupBy == "projects" {
		names = d.projectNames(ctx)
	}
	labels := make([]string, len(counts))
	values := make([]int64, len(counts))
	for i, c := range counts {
		labels[i], values[i] = c.Value, c.Count
		if names != nil {
			labels[i] = nameOf(names, c.Value)
		}
		if labels[i] == "" {
			labels[i] = "(none)"
		}
	}
	label := named(data.NewField(strings.ToLower(groupNames[qm.GroupBy]), nil, labels), groupNames[qm.GroupBy])
	switch qm.GroupBy {
	case "severities":
		label.Config.Mappings = severityMapping
	case "types":
		label.Config.Mappings = typeMapping
	}
	frame := data.NewFrame("issue counts", label, named(data.NewField("issues", nil, values), "Issues"))
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable, Custom: map[string]any{"total": total}}
	return data.Frames{frame}, nil
}

// eventText describes one analysis event in a sentence fragment.
func eventText(e sonar.Event) string {
	var text string
	switch e.Category {
	case "VERSION":
		text = "Version " + e.Name
	case "QUALITY_GATE":
		text = "Quality gate: " + e.Name
	case "QUALITY_PROFILE":
		text = "Quality profile: " + e.Name
	default:
		text = e.Name
	}
	if e.Description != "" {
		text += " (" + e.Description + ")"
	}
	return text
}

// analyses returns the analyses of the selected projects in the time range,
// newest first. With categories, only analyses that have such an event are
// returned. The field names (time, text, tags) are the ones Grafana's
// annotations pick up without mapping.
func (d *Datasource) analyses(ctx context.Context, qm *models.QueryModel, tr backend.TimeRange) (data.Frames, error) {
	names := d.projectNames(ctx)
	lists, err := forEach(ctx, qm.Projects, func(ctx context.Context, project string) ([]sonar.Analysis, error) {
		return d.client.Analyses(ctx, project, qm.Branch, tr.From, tr.To)
	})
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, c := range qm.Categories {
		wanted[c] = true
	}

	type row struct {
		t                                       time.Time
		project, version, text, tags, rev, pkey string
	}
	var rows []row
	for i, project := range qm.Projects {
		for _, a := range lists[i] {
			t, ok := sonar.ParseTime(a.Date)
			if !ok {
				continue
			}
			var texts, cats []string
			for _, e := range a.Events {
				if len(wanted) > 0 && !wanted[e.Category] {
					continue
				}
				texts = append(texts, eventText(e))
				if !containsString(cats, e.Category) {
					cats = append(cats, e.Category)
				}
			}
			if len(wanted) > 0 && len(texts) == 0 {
				continue
			}
			text := strings.Join(texts, "; ")
			if text == "" {
				text = "Analysis"
			}
			rows = append(rows, row{t, nameOf(names, project), a.ProjectVersion, text, strings.Join(cats, ","), a.Revision, project})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].t.After(rows[b].t) })

	n := len(rows)
	times, project, version := make([]time.Time, n), make([]string, n), make([]string, n)
	text, tags, rev, pkey := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, r := range rows {
		times[i], project[i], version[i], text[i], tags[i], rev[i], pkey[i] = r.t, r.project, r.version, r.text, r.tags, r.rev, r.pkey
	}
	frame := data.NewFrame("analyses",
		named(data.NewField("time", nil, times), "Time"),
		named(data.NewField("project", nil, project), "Project"),
		named(data.NewField("version", nil, version), "Version"),
		named(data.NewField("text", nil, text), "Events"),
		named(data.NewField("tags", nil, tags), "Categories"),
		named(data.NewField("revision", nil, rev), "Revision"),
		named(data.NewField("key", nil, pkey), "Project key"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	if n == 0 {
		found := 0
		for _, l := range lists {
			found += len(l)
		}
		if found == 0 {
			addNotice(frame, d.emptyRangeNotice(ctx, qm.Projects))
		} else {
			addNotice(frame, fmt.Sprintf("None of the %d analyses in the selected time range has one of the chosen events. Clear the Events field to see every analysis.", found))
		}
	}
	return data.Frames{frame}, nil
}

// projectList returns the projects the token can see.
func (d *Datasource) projectList(ctx context.Context) (data.Frames, error) {
	projects, total, err := d.projects(ctx)
	if err != nil {
		return nil, err
	}
	n := len(projects)
	name, key, tags, visibility := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	last := make([]*time.Time, n)
	for i, p := range projects {
		name[i], key[i], tags[i], visibility[i] = p.Name, p.Key, strings.Join(p.Tags, ", "), p.Visibility
		if t, ok := sonar.ParseTime(p.AnalysisDate); ok {
			last[i] = &t
		}
	}
	nameField := named(data.NewField("name", nil, name), "Project")
	nameField.Config.Links = []data.DataLink{{Title: "Open in SonarQube", URL: d.settings.URL + "/dashboard?id=${__data.fields.key:percentencode}", TargetBlank: true}}
	frame := data.NewFrame("projects",
		nameField,
		named(data.NewField("key", nil, key), "Key"),
		named(data.NewField("last_analysis", nil, last), "Last analysis"),
		named(data.NewField("tags", nil, tags), "Tags"),
		named(data.NewField("visibility", nil, visibility), "Visibility"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	if total > n {
		addNotice(frame, fmt.Sprintf("Showing the first %d of %d projects.", n, total))
	}
	return data.Frames{frame}, nil
}

// metricList returns the metrics the server knows.
func (d *Datasource) metricList(ctx context.Context) (data.Frames, error) {
	metrics, err := d.metrics(ctx)
	if err != nil {
		return nil, err
	}
	sorted := append([]sonar.Metric(nil), metrics...)
	sort.SliceStable(sorted, func(a, b int) bool { return strings.ToLower(sorted[a].Name) < strings.ToLower(sorted[b].Name) })
	n := len(sorted)
	name, key, domain, typ, desc := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, m := range sorted {
		name[i], key[i], domain[i], typ[i], desc[i] = m.Name, m.Key, m.Domain, m.Type, m.Description
	}
	frame := data.NewFrame("metrics",
		named(data.NewField("name", nil, name), "Metric"),
		named(data.NewField("key", nil, key), "Key"),
		named(data.NewField("domain", nil, domain), "Domain"),
		named(data.NewField("type", nil, typ), "Type"),
		named(data.NewField("description", nil, desc), "Description"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	return data.Frames{frame}, nil
}

// branches returns the branches of the selected projects.
func (d *Datasource) branches(ctx context.Context, qm *models.QueryModel) (data.Frames, error) {
	names := d.projectNames(ctx)
	lists, err := forEach(ctx, qm.Projects, func(ctx context.Context, project string) ([]sonar.Branch, error) {
		return d.client.Branches(ctx, project)
	})
	if err != nil {
		return nil, err
	}
	var (
		branch, project, typ, gate []string
		main                       []bool
		last                       []*time.Time
	)
	for i, key := range qm.Projects {
		for _, b := range lists[i] {
			branch, project, typ = append(branch, b.Name), append(project, nameOf(names, key)), append(typ, b.Type)
			main, gate = append(main, b.IsMain), append(gate, b.Status.QualityGateStatus)
			if t, ok := sonar.ParseTime(b.AnalysisDate); ok {
				last = append(last, &t)
			} else {
				last = append(last, nil)
			}
		}
	}
	gateField := named(data.NewField("quality_gate", nil, orEmpty(gate)), "Quality gate")
	gateField.Config.Mappings = levelMapping
	frame := data.NewFrame("branches",
		named(data.NewField("name", nil, orEmpty(branch)), "Branch"),
		named(data.NewField("project", nil, orEmpty(project)), "Project"),
		named(data.NewField("main", nil, append([]bool{}, main...)), "Main branch"),
		named(data.NewField("type", nil, orEmpty(typ)), "Type"),
		gateField,
		named(data.NewField("last_analysis", nil, append([]*time.Time{}, last...)), "Last analysis"),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}
	return data.Frames{frame}, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func named(f *data.Field, display string) *data.Field {
	if f.Config == nil {
		f.Config = &data.FieldConfig{}
	}
	f.Config.DisplayName = display
	return f
}

func addNotice(frame *data.Frame, text string) {
	if text != "" {
		frame.AppendNotices(data.Notice{Severity: data.NoticeSeverityInfo, Text: text})
	}
}
