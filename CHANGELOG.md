# Changelog

## 0.1.1 (2026-11-21)

### Changed

- The settings page shows the secure socks proxy setting without an
  explanatory paragraph, as other data sources do.

## 0.1.0 (not released)

First version.

### Added

- Works with SonarQube Server (including Community Build) and SonarQube Cloud
  (EU and US regions), with a user token or, for public projects on Cloud,
  anonymously.
- Queries: metric history as time series; current metrics per project;
  issues; issue counts by severity, type, project, rule and more; analyses
  with their events; the list of projects.
- Readable values: units on percentages and durations, A to E ratings and
  Passed/Failed quality gates with colours, links from projects and issues to
  SonarQube.
- Template variables for projects, metrics and branches; annotations from
  new versions and quality gate changes; alerting.
- Private networks: requests can go through Grafana's secure socks proxy
  (Grafana Cloud's Private data source connect).
- Custom CA and client certificates for a self-hosted SonarQube.
