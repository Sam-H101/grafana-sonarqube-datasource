# Changelog

## 0.2.0 (unreleased)

### Added

- **Quality gate conditions** query: one row per condition of each project's
  quality gate, with its value and what the gate requires, so a dashboard can
  show why a gate failed. A switch keeps only the failed conditions.
- **Security hotspots** query: the hotspots of the selected projects, highest
  review priority first, filtered by review status, with a link to each.
- **Pull requests** query: the pull requests SonarQube has analysed, with
  their quality gate. Metric history, current metrics, issues, issue counts,
  quality gate conditions and hotspots can look at one pull request instead
  of a branch. Needs SonarQube Developer Edition or higher, or SonarQube
  Cloud.
- Project tags: the **Projects** query and the Projects template variable can
  be limited to projects that have one of the chosen tags, so one dashboard
  can serve each team.
- Template variable for the pull requests of a project.

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
