package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const healthTimeout = 20 * time.Second

// CheckHealth backs "Save & test". It checks, in order, that the server is
// reachable and is SonarQube, that the token is accepted, and that projects
// can be listed, and says how many the token can see.
func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	fail := func(err error) (*backend.CheckHealthResult, error) {
		p := d.explain(err)
		backend.Logger.Warn("SonarQube health check failed", "status", int(p.status), "error", logSafe(p.message))
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: p.message}, nil
	}
	if d.settingsErr != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: d.explain(d.settingsErr).message}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	status, err := d.client.Status(ctx)
	if err != nil {
		return fail(err)
	}
	if status.Status != "UP" {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("SonarQube at %s is reachable but not ready (status %s). Try again when it has finished starting.", d.settings.URL, status.Status),
		}, nil
	}

	// A bad token is reported here by name: the project list below would
	// only say "unauthorized" on Server, and on Cloud it would silently show
	// the organization's public projects.
	if d.client.HasToken() {
		valid, err := d.client.ValidToken(ctx)
		if err != nil {
			return fail(err)
		}
		if !valid {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: d.product() + " rejected the token. It may be mistyped, expired or revoked: " + tokenHelp,
			}, nil
		}
	}

	_, total, err := d.client.Projects(ctx, 1)
	if err != nil {
		return fail(err)
	}

	where := "SonarQube Server " + status.Version
	if d.settings.Cloud {
		where = "SonarQube Cloud, organization " + d.settings.Organization
	}
	who := "The token"
	if !d.client.HasToken() {
		who = "Anonymous access"
	}
	msg := fmt.Sprintf("Connected to %s. %s can see %d project%s.", where, who, total, plural(total))
	switch {
	case total == 0 && d.client.HasToken():
		msg += " Give the token's user the Browse permission on the projects you want to query."
	case total == 0:
		msg += " Enter a user token to see private projects."
	case !d.client.HasToken() && d.settings.Cloud:
		msg += " Only public projects are visible without a token."
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: msg}, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
