# Testing the SonarQube data source

How to try the plugin from scratch. No account or licence is needed: the test
environment runs the free SonarQube Community Build in Docker and fills it
with sample projects.

## The test environment

Requires Docker with Compose v2 and a Bash shell. The build tools run in a
container, so nothing else has to be installed.

```sh
git clone https://github.com/Sam-H101/grafana-sonarqube-datasource.git
cd grafana-sonarqube-datasource
devenv/run.sh npm ci
devenv/run.sh npm run build
devenv/run.sh mage -v build:linux
docker compose up -d --build --wait
```

The first start takes about three minutes: SonarQube starts, then a one-shot
`seed` service analyses three sample projects several times each, with
backdated dates, so there is history to look at.

Open http://localhost:3000. No login is needed; this development setup gives
anonymous users the Admin role and binds to localhost only. SonarQube itself
is at http://localhost:9000 (admin / admin).

Already provisioned:

| Data source | What it shows |
| --- | --- |
| **SonarQube** | The local server, with a token the seed created |
| **SonarQube (private network)** | The same server, reachable only through a SOCKS proxy: the path Grafana Cloud's Private data source connect uses |
| **SonarQube Cloud (public)** | sonarcloud.io without a token: the public projects of the `sonarsource` organization (needs internet access) |

- Dashboard **SonarQube overview**: a table of projects with quality gate and
  ratings, coverage and bug trends, issue counts, releases, and the newest
  issues, with a project variable and annotations.
- Alert rule **Coverage below 50%**, which fires for the Legacy Billing
  sample project.

Stop and remove everything with `docker compose down -v`.

## What to try

**Save & test.** Open **Connections > Data sources > SonarQube** and click
**Save & test**: `Connected to SonarQube Server 26.11.… The token can see 3
projects.`

**Metric history.** In a new panel, choose the **SonarQube** data source and
set the time range to **Last 90 days**. Choose project **Shop API**: coverage
rises from about 35% to about 80% over six analyses. Add metric **Bugs** and
project **Shop Web** for four series.

With the default range of the last 6 hours the panel is empty, and the query
editor says when the latest analysis was.

**Current metrics.** Change **Query** to **Current metrics** and the
visualization to **Table**: one row per project, with the quality gate shown
as Passed or Failed and project names linking to SonarQube.

**Quality gate conditions.** Change **Query** to **Quality gate conditions**
and the visualization to **Table**: each project's conditions, the failed
ones first. Shop Web and Legacy Billing fail on **New Issues** (required: at
most 0). Turn on **Failed only** to keep just those rows.

**Issues.** Change **Query** to **Issues**. Filter by **Severity** or
**Type**, switch **Status** between Open, Resolved and All, and turn on
**Created in time range**. Each message links to the issue in SonarQube.

**Issue counts.** Change **Query** to **Issue counts** and **Group by** to
Severity, Type, Project or Rule. A bar chart suits it.

**Analyses and events.** Change **Query** to **Analyses and events** and
choose a project: one row per new version or quality gate change.

**Projects by tag.** Change **Query** to **Projects** and choose the tag
`backend` under **Tags**: Shop API and Legacy Billing remain. The same
filter is on the Projects template variable.

**Security hotspots and pull requests.** The local server is SonarQube
Community Build, which has no hotspot rules in this version and does not
analyse pull requests: **Security hotspots** is empty and says why, and
**Pull requests** explains that it needs a paid edition or SonarQube Cloud.
To see both with data, use the **SonarQube Cloud (public)** data source and
project `SonarSource_sonarqube-mcp-server`: **Pull requests** lists its open
pull requests, and **Security hotspots** with **Status** set to **All** lists
its reviewed hotspots. Enter one of the pull request numbers in **Pull
request** on a *Current metrics* query to see that pull request's values.

**Errors.** In the Projects field, type a key that does not exist, such as
`no:such`, and accept it: the panel shows SonarQube's own message. In the
data source settings, a wrong token, a wrong address and an address that is
not SonarQube each get their own explanation from **Save & test**.

**Template variable.** The dashboard's **Project** variable lists projects by
name. Selecting one filters every panel; **All** shows all three.

**Annotations.** The dashboard's coverage and bug panels are marked with each
project's releases and quality gate changes. Toggle them with **Releases and
quality gate changes** at the top.

**Alerting.** **Alerting > Alert rules** shows **Coverage below 50%** firing
for Legacy Billing and normal for Shop API.

## SonarQube Cloud

Add a data source, choose **SonarQube Cloud**, and enter an organization key.
With no token it reads the organization's public projects; `sonarsource` is
an organization with many. **Save & test** reports
`Connected to SonarQube Cloud, organization sonarsource. Anonymous access can
see … projects.`

With a token of your own, private projects of your organization appear too.

## Private data source connect

The provisioned **SonarQube (private network)** data source points at
`http://sonarqube-private:9000`, a name that resolves only on a Docker network
Grafana is not on. Its requests reach SonarQube because the data source has
the secure socks proxy turned on, and the dev stack's Grafana is configured
with a SOCKS proxy that is on both networks.

To see the difference: create a data source with that URL and the token
(printed by `docker compose run --rm --no-deps -T --entrypoint cat seed
/seed-out/token`). **Save & test** fails with "host not found". Turn on the
switch under **Private network** and it connects.

The local proxy is plain SOCKS5. Grafana Cloud's service adds mutual TLS and
an agent on your network, but the plugin's side is the same: it asks Grafana's
SDK for an HTTP client, and the SDK routes it through the proxy.

## Automated tests

The same stack runs the automated tests, as CI does on every commit against
the supported Grafana versions:

```sh
devenv/run.sh npx playwright install chromium   # once
devenv/run.sh npm run e2e                                          # browser tests
devenv/run.sh go test -tags integration ./tests/integration/...    # backend against SonarQube
```

Set `SONARCLOUD_TESTS=1` to include the tests that use sonarcloud.io. More
about the development environment is in
[devenv/README.md](../devenv/README.md).
