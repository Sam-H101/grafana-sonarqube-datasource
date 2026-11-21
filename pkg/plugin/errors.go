package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/models"
	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// problem is an error translated for a user, with the status and error
// source Grafana should record.
type problem struct {
	status     backend.Status
	message    string
	downstream bool
}

const tokenHelp = "generate a new user token in SonarQube under My Account > Security, and paste it into the data source settings."

// explain turns any error from settings, queries or the SonarQube client into
// a message for the panel or the health check. Messages never contain the
// token (the URL may not carry credentials) or raw stack traces.
func (d *Datasource) explain(err error) problem {
	var (
		apiErr  *sonar.APIError
		netErr  net.Error
		dnsErr  *net.DNSError
		opErr   *net.OpError
		urlErr  *url.Error
		unkCA   x509.UnknownAuthorityError
		hostErr x509.HostnameError
		certErr x509.CertificateInvalidError
		recErr  tls.RecordHeaderError
		verErr  *tls.CertificateVerificationError
	)
	hasToken := d.client != nil && d.client.HasToken()
	switch {
	case errors.Is(err, models.ErrInvalidSettings):
		return problem{backend.StatusBadRequest, "Configuration problem: " + strings.TrimPrefix(err.Error(), models.ErrInvalidSettings.Error()+": "), true}
	case errors.Is(err, models.ErrInvalidQuery):
		msg := strings.TrimPrefix(err.Error(), models.ErrInvalidQuery.Error()+": ")
		return problem{backend.StatusBadRequest, strings.ToUpper(msg[:1]) + msg[1:] + ".", true}
	case errors.Is(err, sonar.ErrUnauthorized):
		if hasToken {
			return problem{backend.StatusUnauthorized, d.product() + " rejected the token. It may be mistyped, expired or revoked: " + tokenHelp, true}
		}
		return problem{backend.StatusUnauthorized, "This " + d.product() + " requires authentication. Enter a user token in the data source settings (create one in SonarQube under My Account > Security).", true}
	case errors.Is(err, sonar.ErrForbidden):
		return problem{backend.StatusForbidden, "The token's user is not allowed to read this. Give that user the Browse permission on the projects you want to query.", true}
	case errors.As(err, &apiErr):
		msg := strings.TrimSuffix(apiErr.Message, ".")
		if apiErr.NotFound() {
			msg += ". Check the key, and that the token's user can browse the project"
		}
		return problem{backend.StatusBadRequest, d.product() + ": " + msg + ".", true}
	case errors.Is(err, sonar.ErrNotSonarQube):
		return problem{backend.StatusBadGateway, "The server at " + d.url() + " did not answer like SonarQube. Check the URL: use the address you open SonarQube at in a browser, including any path prefix such as /sonarqube.", true}
	case errors.Is(err, sonar.ErrResponseTooLarge):
		return problem{backend.StatusBadRequest, "The answer from SonarQube is too large. Select fewer projects or metrics, or a shorter time range.", true}
	case errors.Is(err, context.DeadlineExceeded):
		secs := 0
		if d.settings != nil {
			secs = int(d.settings.Timeout.Seconds())
		}
		return problem{backend.StatusTimeout, fmt.Sprintf("SonarQube did not answer within %d seconds. Select fewer projects or a shorter time range, or raise the request timeout in the data source settings.", secs), true}
	case errors.Is(err, context.Canceled):
		return problem{backend.StatusTimeout, "The query was cancelled.", true}
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return problem{backend.StatusBadGateway, "SonarQube at " + d.url() + " closed the connection before answering. It may be restarting.", true}
	case errors.As(err, &unkCA), errors.As(err, &hostErr), errors.As(err, &certErr), errors.As(err, &verErr), errors.As(err, &recErr):
		return problem{backend.StatusBadGateway, "TLS error connecting to SonarQube: " + innermost(err) + ". Check the URL scheme and the TLS settings.", true}
	case strings.Contains(err.Error(), "malformed HTTP response"):
		// Typically TLS alert bytes: an http:// URL pointing at an HTTPS port.
		return problem{backend.StatusBadGateway, "The server at " + d.url() + " did not answer with HTTP. If SonarQube is served over HTTPS, change the URL to https://.", true}
	case strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return problem{backend.StatusBadGateway, "SonarQube at " + d.url() + " answered with plain HTTP. Change the URL to http://, or put SonarQube behind HTTPS.", true}
	case errors.As(err, &dnsErr):
		return problem{backend.StatusBadGateway, "Cannot reach SonarQube at " + d.url() + ": host not found." + d.proxyHint(), true}
	case errors.As(err, &opErr), errors.As(err, &netErr), errors.As(err, &urlErr):
		return problem{backend.StatusBadGateway, "Cannot reach SonarQube at " + d.url() + ": " + innermost(err) + "." + d.proxyHint(), true}
	default:
		return problem{backend.StatusInternal, err.Error(), false}
	}
}

// proxyHint reminds users of a hosted Grafana that a private SonarQube needs
// a route to it.
func (d *Datasource) proxyHint() string {
	if d.settings != nil && d.settings.Cloud {
		return ""
	}
	return " If SonarQube is on a private network and Grafana is hosted elsewhere (for example Grafana Cloud), turn on Private data source connect in the data source settings."
}

// logSafe shortens a user-facing message for logs.
func logSafe(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

func (d *Datasource) url() string {
	if d.settings == nil {
		return "the configured URL"
	}
	return d.settings.URL
}

// innermost strips url.Error's "Get \"...\":" prefix and similar wrapping.
func innermost(err error) string {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err.Error()
		}
		err = u
	}
}

func (p problem) response() backend.DataResponse {
	src := backend.ErrorSourcePlugin
	if p.downstream {
		src = backend.ErrorSourceDownstream
	}
	return backend.ErrDataResponseWithSource(p.status, src, p.message)
}
