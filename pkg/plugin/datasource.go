// Package plugin holds the Grafana SDK handlers. It wires settings and the
// SonarQube client together and turns API answers into data frames.
package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/models"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/httpclient"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
)

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ backend.CallResourceHandler   = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// Datasource is one configured SonarQube data source. Instances are created
// per settings version by the SDK's instance manager and used concurrently.
type Datasource struct {
	settings *models.Settings
	client   *sonar.Client
	// settingsErr is a configuration problem, reported on every request
	// instead of failing instance creation with an opaque error.
	settingsErr error

	cache      catalogue
	resOnce    sync.Once
	resHandler backend.CallResourceHandler
}

// NewDatasource is the SDK instance factory.
func NewDatasource(ctx context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	settings, err := models.LoadSettings(s)
	if err != nil {
		return &Datasource{settingsErr: err}, nil
	}

	// HTTPClientOptions carries the TLS settings and, when the data source
	// has the secure socks proxy turned on (Grafana Cloud's Private data
	// source connect), the proxy the SDK routes every request through.
	opts, err := s.HTTPClientOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("http client options: %w", err)
	}
	// The token is sent by the SonarQube client, not by the SDK (which would
	// add Basic auth whenever Grafana's own user field is set).
	opts.BasicAuth = nil
	if opts.Timeouts == nil {
		opts.Timeouts = &httpclient.DefaultTimeoutOptions
	}
	timeouts := *opts.Timeouts
	// Per-request deadlines come from the context; this only backstops them.
	timeouts.Timeout = settings.Timeout + 15*time.Second
	opts.Timeouts = &timeouts

	hc, err := httpclient.New(opts)
	if err != nil {
		return nil, fmt.Errorf("http client: %w", err)
	}
	client, err := sonar.NewClient(sonar.Config{
		BaseURL:      settings.URL,
		Token:        settings.Token,
		Organization: settings.Organization,
		Cloud:        settings.Cloud,
	}, hc)
	if err != nil {
		return &Datasource{settingsErr: fmt.Errorf("%w: %w", models.ErrInvalidSettings, err)}, nil
	}
	return &Datasource{settings: settings, client: client}, nil
}

// Dispose is called when settings change and a new instance replaces this one.
func (d *Datasource) Dispose() {}

// product names the kind of SonarQube this data source points at.
func (d *Datasource) product() string {
	if d.settings != nil && d.settings.Cloud {
		return "SonarQube Cloud"
	}
	return "SonarQube"
}
