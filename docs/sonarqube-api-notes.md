# SonarQube Web API: observed behaviour

Observed on 2026-11-21 with `curl` against SonarQube Community Build
26.11.0.134175 (the `sonarqube:26.11.0.134175-community` image) and against
SonarQube Cloud at `https://sonarcloud.io` (anonymous, public projects of the
`sonarsource` organization). The integration tests also pass against
SonarQube 9.9.8 (`sonarqube:9.9-community`). The client in `pkg/sonar` relies on what is
written here, and `pkg/sonar/sonartest/testdata/` holds the captured
responses.

## 1. Authentication

- A user token is accepted as the user name of HTTP Basic authentication with
  an empty password (`Authorization: Basic base64("<token>:")`), on Server and
  on Cloud. Server 26.11 also accepts `Authorization: Bearer <token>`, but
  Server 9.9.8 answers `401` to it, so the plugin uses Basic.
- A wrong token: `401` with an empty body on every data endpoint, on both.
- `GET api/authentication/validate` always answers `200`:
  - with a valid token `{"valid":true}`, with a wrong one `{"valid":false}`;
  - with no credentials, `{"valid":false}` on a Server that forces
    authentication and `{"valid":true}` on Cloud.

  So it tells a bad token from a good one, but not whether anonymous access
  will show anything useful.
- `GET api/system/status` needs no credentials. On Cloud its `version` is a
  fixed internal number (`8.0.0.…`) and is not shown to users.
- Server with no credentials: `401` on data endpoints. Cloud with no
  credentials: public projects only.

## 2. Projects

- `GET api/components/search_projects?ps=500&p=1&f=analysisDate&s=name`
  returns `key`, `name`, `analysisDate`, `tags`, `visibility`.
- On Cloud it must be scoped with `organization=<key>`. Without it, it
  returns every public project on the service (the total is capped at
  10,000). An unknown organization is `404` with
  `No organization for key '<key>'`.
- Project names are arbitrary text, including markup; they are only ever
  rendered as text.

## 3. Metrics and measures

- `GET api/metrics/search?ps=500` lists metrics with `key`, `name`, `domain`,
  `type` and `hidden`. Types seen: `INT`, `FLOAT`, `PERCENT`, `BOOL`,
  `STRING`, `MILLISEC`, `DATA`, `LEVEL`, `DISTRIB`, `RATING`, `WORK_DUR`.
- Every measure value is a JSON string, whatever its type. Ratings are
  `"1.0"` to `"5.0"` (A to E); `alert_status` is `OK`, `ERROR`, `WARN` or
  `NONE`; `WORK_DUR` is in minutes.
- `GET api/measures/search_history?component=&metrics=&from=&to=&branch=&ps=1000&p=`
  returns, per metric, `history: [{date, value}]`, oldest first. `value` is
  absent where the metric had no value. `paging.total` counts analyses.
  `from` and `to` take `2006-01-02T15:04:05-0700` (the `+` must be
  percent-encoded).
- An unknown project is `404` (`Component key 'x' not found`); an unknown
  metric `400` (`Metrics x are not found`); an unknown branch `404`
  (`Component 'p' on branch 'b' not found`).
- `GET api/measures/search?projectKeys=&metricKeys=` returns current values
  for many projects at once (main branch only). Metrics a project has no
  value for are simply missing. "New code" metrics carry the value in
  `period.value` or `periods[0].value`, not `value`.
- `GET api/measures/component?component=&metricKeys=&branch=` is the
  per-project form, used when a branch is given.

## 4. Issues

- `GET api/issues/search` filters by `componentKeys` (comma-separated project
  keys), `severities`, `types`, `resolved`, `createdAfter`, `createdBefore`,
  `branch`; sorts with `s=CREATION_DATE&asc=false`; pages with `ps` (max 500)
  and `p`.
- **Parameter name:** Server accepts both `componentKeys` and the newer
  `components`. Cloud accepts only `componentKeys` and answers `400` to
  `components`. The plugin sends `componentKeys`.
- On Cloud a search needs `organization`, `componentKeys` or another scope;
  the plugin always sends the organization.
- Only the first 10,000 results can be read: a page past that is `400`
  (`Can return only the first 10000 results.`).
- `facets=<name>` adds counts per value. Names accepted by both without a
  project filter: `severities`, `types`, `projects`, `rules`, `tags`,
  `languages`, `assignees`, `author` (singular), `resolutions`, `statuses`.
  `files` needs a project filter on Server and does not exist on Cloud
  (`fileUuids` there).
- Security hotspots are not issues; they have their own API
  (`api/hotspots/search`).

## 5. Analyses and branches

- `GET api/project_analyses/search?project=&from=&to=&branch=&ps=500`
  returns analyses, newest first, each with `date`, `projectVersion`,
  `revision` and `events: [{category, name, description}]`. Categories seen:
  `VERSION`, `QUALITY_GATE`, `SQ_UPGRADE`.
- `GET api/project_branches/list?project=` returns branches with `name`,
  `isMain`, `type`, `status.qualityGateStatus`, `analysisDate`. Community
  Build has only the main branch.

## 6. Seeding a test server

- `sonar.projectDate` backdates an analysis; dates must increase from one
  analysis of a project to the next.
- The scanner's token property is `sonar.token` from Server 10.0 and
  `sonar.login` before it.
- A backdated analysis that closes an issue more than 30 days in the past
  fails on the server (`NoSuchElementException` in `IssueIndexer`): the issue
  is purged in the same run that closes it. Raising
  `sonar.dbcleaner.daysBeforeDeletingClosedIssues` avoids it
  (`devenv/sonarqube/seed.py`).
