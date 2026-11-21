package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/models"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

const (
	projectsTTL = time.Minute
	metricsTTL  = 10 * time.Minute
	// maxListedProjects bounds the project list kept for the editor's
	// dropdown, for project names in results, and for "all projects".
	maxListedProjects = 1000
	resourceTimeout   = 20 * time.Second
)

// catalogue caches the project and metric lists of one data source instance.
// Both are needed by almost every query (names, metric types) and by the
// query editor.
type catalogue struct {
	mu            sync.Mutex
	projects      []sonar.Project
	projectsTotal int
	projectsAt    time.Time
	metrics       []sonar.Metric
	metricsAt     time.Time
}

// projects returns up to maxListedProjects projects and the total number the
// token can see.
func (d *Datasource) projects(ctx context.Context) ([]sonar.Project, int, error) {
	d.cache.mu.Lock()
	if d.cache.projects != nil && time.Since(d.cache.projectsAt) < projectsTTL {
		p, t := d.cache.projects, d.cache.projectsTotal
		d.cache.mu.Unlock()
		return p, t, nil
	}
	d.cache.mu.Unlock()

	projects, total, err := d.client.Projects(ctx, maxListedProjects)
	if err != nil {
		return nil, 0, err
	}
	if projects == nil {
		projects = []sonar.Project{}
	}
	d.cache.mu.Lock()
	d.cache.projects, d.cache.projectsTotal, d.cache.projectsAt = projects, total, time.Now()
	d.cache.mu.Unlock()
	return projects, total, nil
}

// metrics returns the server's metrics.
func (d *Datasource) metrics(ctx context.Context) ([]sonar.Metric, error) {
	d.cache.mu.Lock()
	if d.cache.metrics != nil && time.Since(d.cache.metricsAt) < metricsTTL {
		m := d.cache.metrics
		d.cache.mu.Unlock()
		return m, nil
	}
	d.cache.mu.Unlock()

	metrics, err := d.client.Metrics(ctx)
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = []sonar.Metric{}
	}
	d.cache.mu.Lock()
	d.cache.metrics, d.cache.metricsAt = metrics, time.Now()
	d.cache.mu.Unlock()
	return metrics, nil
}

// CallResource serves the lists the query editor's dropdowns are built from:
//
//	GET /projects            {"projects":[{"key","name"}], "total": n}
//	GET /metrics             [{"key","name","domain","type","description"}]
//	GET /branches?project=k  [{"name","isMain"}]
//	GET /tags                ["tag", ...] (the tags of the listed projects)
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	d.resOnce.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /projects", d.handleProjects)
		mux.HandleFunc("GET /metrics", d.handleMetrics)
		mux.HandleFunc("GET /branches", d.handleBranches)
		mux.HandleFunc("GET /tags", d.handleTags)
		d.resHandler = httpadapter.New(mux)
	})
	return d.resHandler.CallResource(ctx, req, sender)
}

type projectItem struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (d *Datasource) handleProjects(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	projects, total, err := d.projects(ctx)
	if err != nil {
		d.resourceError(w, err)
		return
	}
	items := make([]projectItem, len(projects))
	for i, p := range projects {
		items[i] = projectItem{Key: p.Key, Name: p.Name}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items, "total": total})
}

// handleTags lists the tags of the listed projects. (SonarQube's own tag
// search is not limited to one organization on SonarQube Cloud.)
func (d *Datasource) handleTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	projects, _, err := d.projects(ctx)
	if err != nil {
		d.resourceError(w, err)
		return
	}
	seen := map[string]bool{}
	tags := []string{}
	for _, p := range projects {
		for _, t := range p.Tags {
			if !seen[t] {
				seen[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(tags)
	writeJSON(w, http.StatusOK, tags)
}

func (d *Datasource) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	metrics, err := d.metrics(ctx)
	if err != nil {
		d.resourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (d *Datasource) handleBranches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := d.resourceContext(w, r)
	if !ok {
		return
	}
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"queryType": models.QueryBranches, "projects": []string{strings.TrimSpace(r.URL.Query().Get("project"))}})
	q, err := models.ParseQuery(raw)
	if err != nil {
		d.resourceError(w, err)
		return
	}
	branches, err := d.client.Branches(ctx, q.Projects[0])
	if err != nil {
		d.resourceError(w, err)
		return
	}
	type item struct {
		Name   string `json:"name"`
		IsMain bool   `json:"isMain"`
	}
	items := make([]item, len(branches))
	for i, b := range branches {
		items[i] = item{Name: b.Name, IsMain: b.IsMain}
	}
	writeJSON(w, http.StatusOK, items)
}

func (d *Datasource) resourceContext(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc, bool) {
	if d.settingsErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": d.explain(d.settingsErr).message})
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), resourceTimeout)
	return ctx, cancel, true
}

func (d *Datasource) resourceError(w http.ResponseWriter, err error) {
	p := d.explain(err)
	backend.Logger.Warn("SonarQube metadata request failed", "error", logSafe(p.message))
	status := http.StatusBadGateway
	if p.status == backend.StatusBadRequest || p.status == backend.StatusUnauthorized || p.status == backend.StatusForbidden {
		status = int(p.status)
	}
	writeJSON(w, status, map[string]string{"message": p.message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
