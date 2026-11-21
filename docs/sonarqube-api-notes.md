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
- Security hotspots are not issues; they have their own API (section 7).

## 5. Analyses and branches

- `GET api/project_analyses/search?project=&from=&to=&branch=&ps=500`
  returns analyses, newest first, each with `date`, `projectVersion`,
  `revision` and `events: [{category, name, description}]`. Categories seen:
  `VERSION`, `QUALITY_GATE`, `SQ_UPGRADE`.
- `GET api/project_branches/list?project=` returns branches with `name`,
  `isMain`, `type`, `status.qualityGateStatus`, `analysisDate`. Community
  Build has only the main branch.

## 6. Quality gate conditions, hotspots, tags and pull requests

- **Quality gate conditions.** The measure `quality_gate_details` (type
  `DATA`) holds the gate as evaluated in the latest analysis, as JSON:
  `{"level":"ERROR","conditions":[{"metric","op","error","actual","level"}]}`.
  `op` is the comparison that fails the condition (`LT`, `GT`). `actual` can
  be an empty string on a pull request. It is on Server 9.9, Server 26.11
  and Cloud, and `api/measures/search` returns it for many projects in one
  request, which `api/qualitygates/project_status` cannot do.
- **Hotspots.** `GET api/hotspots/search` needs one project. The parameter
  is `projectKey` on Cloud and Server 9.9 and `project` on Server 26.11; each
  ignores the other name, so the plugin sends both. `status` is `TO_REVIEW`
  or `REVIEWED`; without it both are returned. `branch` and `pullRequest`
  are accepted. There is no sort parameter.
- **Server 26.11 has no hotspot rules.** `api/rules/search?facets=types`
  counts 0 rules of type `SECURITY_HOTSPOT`; the former hotspot rules carry
  the tag `former-hotspot` and are of type `VULNERABILITY`. The hotspot
  search still answers, with an empty list. Server 9.9 has 43 Python hotspot
  rules, and Cloud projects have hotspots.
- **Tags.** `api/components/search_projects?filter=tags IN (a,b)` returns the
  projects with one of the tags. The values must not be quoted: Cloud
  matches nothing for `tags IN ("a","b")`. `api/project_tags/search` is not
  limited to the organization on Cloud (it returns every public tag), so the
  plugin offers the tags found on the listed projects instead.
- **Pull requests.** `GET api/project_pull_requests/list?project=` returns
  `key`, `title`, `branch`, `base`, `target`, `url`, `analysisDate` and
  `status: {qualityGateStatus, bugs, vulnerabilities, codeSmells}`. On
  Community Build (9.9 and 26.11) the endpoint does not exist: `404` with
  `Unknown url : /api/project_pull_requests/list`.
- `pullRequest=<key>` is accepted by `api/measures/component`,
  `api/measures/search_history`, `api/issues/search` and
  `api/hotspots/search` (checked on Cloud). `api/project_analyses/search`
  ignores it and answers for the project.

## 7. Seeding a test server

- `sonar.projectDate` backdates an analysis; dates must increase from one
  analysis of a project to the next.
- The scanner's token property is `sonar.token` from Server 10.0 and
  `sonar.login` before it.
- A backdated analysis that closes an issue more than 30 days in the past
  fails on the server (`NoSuchElementException` in `IssueIndexer`): the issue
  is purged in the same run that closes it. Raising
  `sonar.dbcleaner.daysBeforeDeletingClosedIssues` avoids it
  (`devenv/sonarqube/seed.py`).
