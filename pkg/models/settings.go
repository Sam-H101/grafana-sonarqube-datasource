// Package models parses data source settings and query models. It does no I/O.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const (
	DefaultTimeoutSeconds = 30
	MaxTimeoutSeconds     = 300
)

// Settings is the validated configuration of one data source instance.
type Settings struct {
	// URL is the base URL of SonarQube Server or SonarQube Cloud, without a
	// trailing slash. It may include a context path (https://host/sonarqube).
	URL string
	// Cloud reports whether URL is a SonarQube Cloud host.
	Cloud bool
	// Organization is the SonarQube Cloud organization key. Required on
	// Cloud, where projects and issues are scoped by organization; unused on
	// Server.
	Organization string
	// Token is a SonarQube user token. Empty means anonymous access, which
	// works for public projects on SonarQube Cloud and for servers that do
	// not force authentication.
	Token   string
	Timeout time.Duration
}

type jsonSettings struct {
	Organization   string `json:"organization"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// ErrInvalidSettings wraps every validation failure so callers can report it
// as a configuration problem.
var ErrInvalidSettings = errors.New("invalid data source settings")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(format, a...))
}

// cloudHosts are the SonarQube Cloud regions (EU and US).
var cloudHosts = map[string]bool{
	"sonarcloud.io":     true,
	"www.sonarcloud.io": true,
	"sonarqube.us":      true,
	"www.sonarqube.us":  true,
}

// IsCloudHost reports whether host (without port) is SonarQube Cloud.
func IsCloudHost(host string) bool {
	return cloudHosts[strings.ToLower(strings.TrimSuffix(host, "."))]
}

// Organization keys on SonarQube Cloud are lower-case letters, digits and
// hyphens; a little slack is allowed for underscores and dots.
var organizationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

// LoadSettings reads and validates instance settings. The URL is Grafana's
// standard data source URL field; the token is secureJsonData.token.
func LoadSettings(src backend.DataSourceInstanceSettings) (*Settings, error) {
	var js jsonSettings
	if len(src.JSONData) > 0 {
		if err := json.Unmarshal(src.JSONData, &js); err != nil {
			return nil, invalid("could not parse settings JSON")
		}
	}

	baseURL, host, err := normalizeURL(src.URL)
	if err != nil {
		return nil, err
	}

	s := &Settings{
		URL:          baseURL,
		Cloud:        IsCloudHost(host),
		Organization: strings.TrimSpace(js.Organization),
		Token:        strings.TrimSpace(src.DecryptedSecureJSONData["token"]),
	}

	if s.Organization != "" && !organizationPattern.MatchString(s.Organization) {
		return nil, invalid("the organization key may only contain letters, digits, hyphens, underscores and dots")
	}
	if s.Cloud && s.Organization == "" {
		return nil, invalid("SonarQube Cloud needs an organization key. It is the last part of your organization's URL, for example https://sonarcloud.io/organizations/my-org")
	}
	if strings.ContainsAny(s.Token, " \t\r\n") {
		return nil, invalid("the token contains whitespace; paste the token only")
	}

	switch {
	case js.TimeoutSeconds == 0:
		s.Timeout = DefaultTimeoutSeconds * time.Second
	case js.TimeoutSeconds < 0 || js.TimeoutSeconds > MaxTimeoutSeconds:
		return nil, invalid("the request timeout must be between 1 and %d seconds", MaxTimeoutSeconds)
	default:
		s.Timeout = time.Duration(js.TimeoutSeconds) * time.Second
	}
	return s, nil
}

func normalizeURL(raw string) (base, host string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", invalid("the SonarQube URL is required, for example https://sonarqube.example.com or https://sonarcloud.io")
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", invalid("the SonarQube URL is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", invalid("the SonarQube URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", "", invalid("the SonarQube URL has no host")
	}
	if u.User != nil {
		// Credentials in the URL would end up in logs and error messages.
		return "", "", invalid("put the token in the Token field, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", invalid("the SonarQube URL must not have a query string or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	// People paste the address of a page or of the API; keep the base only.
	for _, suffix := range []string{"/api", "/projects", "/dashboard", "/sessions/new"} {
		u.Path = strings.TrimSuffix(u.Path, suffix)
	}
	u.RawPath = ""
	if IsCloudHost(u.Hostname()) && u.Scheme != "https" {
		return "", "", invalid("SonarQube Cloud is only served over https://")
	}
	return u.String(), u.Hostname(), nil
}
