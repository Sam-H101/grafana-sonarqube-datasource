# SonarQube data source for Grafana

See code quality from [SonarQube](https://www.sonarsource.com/products/sonarqube/)
next to the rest of your dashboards: metric trends, current values across
projects, quality gates, issues, and the releases and quality gate changes
that explain them. It works with **SonarQube Server** (self-hosted, including
the free Community Build) and **SonarQube Cloud**.

The plugin only reads. It talks to SonarQube's Web API from Grafana's server,
so the token never reaches the browser and alert rules work.

## Requirements

- Grafana 13.2 or later.
- SonarQube Server 9.9 or later reachable from the Grafana server, or a
  SonarQube Cloud organization. Tested against SonarQube 9.9 (the long-term
  release), Community Build 26.11 and SonarQube Cloud.
- A SonarQube user token, unless you only read public projects on SonarQube
  Cloud.

## Configuration

Add a data source of type **SonarQube**.

| Setting | Notes |
| --- | --- |
| SonarQube | **SonarQube Server**, **SonarQube Cloud** (sonarcloud.io) or **SonarQube Cloud (US)** (sonarqube.us). Choosing Cloud sets the address for you. |
| URL | Server only: the address you open SonarQube at, including any path prefix, for example `https://sonarqube.example.com` or `https://tools.example.com/sonarqube`. |
| Organization key | Cloud only: the last part of your organization's address, for example `my-org` in `sonarcloud.io/organizations/my-org`. |
| Token | A user token. In SonarQube, open **My Account > Security** and generate a token of type **User**. It is stored encrypted and never sent back to the browser. |
| Private network | Server only: sends requests through Grafana's secure proxy. See [Grafana Cloud and a private SonarQube](#grafana-cloud-and-a-private-sonarqube). |
| TLS | Server only: a custom CA certificate, a client certificate, or (for testing) no verification. |
| Query timeout | Seconds one panel query may take, 1 to 300, default 30. |

**Save & test** reports the server version and how many projects the token
can see, or says what is wrong: an unreachable host, an address that is not
SonarQube, a rejected token, a missing organization.

### What the token needs

The plugin only reads, so the token's user needs **Browse** permission on the
projects you want to query, and nothing more. A dedicated read-only user is a
good idea. The token is sent as HTTP Basic authentication; use an `https://`
address so it is encrypted on the way (the settings page warns when it is
not).

On SonarQube Cloud you can leave the token empty to read only the
organization's public projects.

### Grafana Cloud and a private SonarQube

Grafana Cloud cannot reach a SonarQube inside your network on its own.
[Private data source connect](https://grafana.com/docs/grafana-cloud/connect-externally-hosted/private-data-source-connect/)
solves that: you run Grafana's agent on your network, and Grafana Cloud sends
this data source's requests through it.

1. Set up Private data source connect in your Grafana Cloud stack and start
   the agent somewhere that can reach SonarQube.
2. In this data source's settings, under **Private network**, turn on the
   secure socks proxy. If Grafana offers a choice of network, pick the one
   your agent belongs to.
3. For **URL**, enter SonarQube's address as the agent sees it, for example
   `http://sonarqube.internal:9000`.

Self-hosted Grafana with the secure socks proxy configured works the same
way. SonarQube Cloud is reached directly and does not need this.

### Provisioning

```yaml
apiVersion: 1
datasources:
  - name: SonarQube
    type: smartquinoa1504-sonarqube-datasource
    access: proxy
    url: https://sonarqube.example.com
    jsonData:
      timeoutSeconds: 30
      # enableSecureSocksProxy: true   # Private data source connect
    secureJsonData:
      token: $SONAR_TOKEN

  - name: SonarQube Cloud
    type: smartquinoa1504-sonarqube-datasource
    access: proxy
    url: https://sonarcloud.io # or https://sonarqube.us
    jsonData:
      organization: my-org
    secureJsonData:
      token: $SONARCLOUD_TOKEN
```

## Queries

Choose what to ask for in the **Query** field.

| Query | Returns | Good for |
| --- | --- | --- |
| **Metric history** | Metric values over time, one series per project and metric | Time series of coverage, bugs, technical debt |
| **Current metrics** | The latest value of each metric, one row per project | Overview tables, stat panels, quality gate status |
| **Quality gate conditions** | Each condition of the quality gate: what it measures, the value, what the gate requires, and whether it passed | Showing why a gate failed |
| **Issues** | A list of issues, newest first, with a link to each | Tables of what needs attention |
| **Issue counts** | How many issues there are per severity, type, project, rule, tag, language, assignee, author, resolution or status | Bar and pie charts |
| **Security hotspots** | A list of hotspots, highest review priority first, with a link to each | Review queues |
| **Analyses and events** | Analyses with their events: new versions, quality gate changes | Annotations, release tables |
| **Pull requests** | The pull requests SonarQube has analysed, with their quality gate and new issues | Tables of what is waiting to merge |
| **Projects** | The projects the token can see, or those with one of the chosen tags | Inventory tables |

**Projects** and **Metrics** are dropdowns filled from your SonarQube. You can
also type a project key that is not listed, or choose a dashboard variable.
Leave Projects empty in *Current metrics*, *Quality gate conditions*, *Issues*
and *Issue counts* to cover every project the token can see. *Security
hotspots* and *Pull requests* need at least one project, because SonarQube
lists them one project at a time.

### Time range

*Metric history* and *Analyses and events* return what happened inside the
dashboard's time range. Projects are analysed far less often than most data in
Grafana changes, so a short range (a new panel shows the last 6 hours) is
often empty; the panel's query editor then says when the latest analysis was.
Use a range such as the last 90 days.

*Current metrics*, *Quality gate conditions*, *Security hotspots* and *Pull
requests* always show the latest state, whatever the time range.
*Issues* and *Issue counts* cover issues of any age unless you turn on
**Created in time range**.

### How values are shown

- Percentages, durations and counts carry their unit. Technical debt and
  effort are in minutes.
- Ratings (reliability, security, maintainability) are shown as **A** to **E**
  with SonarQube's colours; the underlying value is 1 to 5.
- The quality gate status is shown as **Passed**, **Failed** or **Warning**;
  the underlying value is `OK`, `ERROR` or `WARN`.
- Project names and issue messages link to SonarQube.

### Quality gate conditions

Each row is one condition of a project's quality gate as SonarQube evaluated
it in the latest analysis: the **Condition** (a metric), its **Value**, what
is **Required** (for example `at least 80%`, `at most 0`, `A or better`) and
its **Status**. Failed conditions come first. Turn on **Failed only** for a
table of just the reasons gates are failing.

### Security hotspots

**Status** selects hotspots still **To review** (the default), those already
**Reviewed**, or **All**. Reviewed hotspots show how they were resolved:
Fixed, Safe or Acknowledged.

Some SonarQube versions have no hotspot rules: SonarQube Community Build
26.11 reports what used to be hotspots as vulnerabilities. There this query
is empty, and the *Issues* query with type **Vulnerability** lists them.

### Branches and pull requests

Leave **Branch** empty for the main branch, or enter a branch name. To look
at a pull request instead, enter its number in **Pull request**; a query uses
one or the other. Both need SonarQube Developer Edition or higher, or
SonarQube Cloud.

The *Pull requests* query lists what can be entered there: each pull request
SonarQube has analysed, with its quality gate. SonarQube deletes a pull
request some time after its last analysis (30 days by default), so the list
is of recent ones.

## Template variables

Create a variable of type **Query** on this data source and choose what it
lists: **Projects** (shows names, the value is the key), **Metrics**, the
**Branches of a project**, or the **Pull requests of a project** (shows the
number and title, the value is the number).

For **Projects** you can choose **Tags**: the variable then lists only the
projects that have one of those tags. A dashboard per team needs nothing
more than a tag per team in SonarQube. The tag can itself be a variable, for
example `$team`.

Use the variable by choosing it in a query's Projects or Metrics field. A
multi-value variable, or **All**, selects several at once, for example one
series per selected project.

## Annotations

Add an annotation query on this data source. It uses *Analyses and events*:
choose the projects and the events you want (new versions and quality gate
changes by default). Each event becomes an annotation at the time of its
analysis, so a drop in coverage lines up with the release that caused it.

## Alerting

Alert rules can use *Metric history*, for example: reduce `coverage` to its
last value and alert when it is below a threshold. Each project is a separate
alert instance, labelled with the project's name. Dashboard variables are not
available in alert rules (a Grafana rule), so name the projects in the query.

## Security

- The plugin makes read requests only. It cannot change projects, issues,
  settings or permissions in SonarQube.
- Anyone who can query this data source sees what the configured token can
  see, including from Explore. Give the token's user access to only the
  projects that Grafana should show, and use Grafana's data source permissions
  to limit who can query it.
- The token and TLS keys are stored in Grafana's encrypted secure settings and
  used only by the plugin's backend. They do not appear in error messages or
  logs.

## Known limitations

- SonarQube returns at most the first 10,000 issues of a search. The *Issues*
  query says when more issues match than it shows; narrow the filters, or use
  *Issue counts* for totals.
- The Projects dropdown lists the first 1,000 projects; type a key to use
  another. With Projects left empty, *Current metrics* covers those first
  1,000.
- One query can name up to 200 projects and 30 metrics.
- Security hotspots are not listed by the *Issues* query (SonarQube keeps them
  apart from issues); use the *Security hotspots* query. Their count is also
  available as the `security_hotspots` metric.
- *Analyses and events* covers the main branch or a branch, not a pull
  request.
- The Tags dropdown offers the tags of the first 1,000 projects; type a tag
  to use another.
- Portfolios and applications (Enterprise Edition) are not listed as
  projects.

## Feedback and contributing

Report bugs and request features in the
[issue tracker](https://github.com/Sam-H101/grafana-sonarqube-datasource/issues). To build or change the
plugin, see [CONTRIBUTING.md](https://github.com/Sam-H101/grafana-sonarqube-datasource/blob/master/CONTRIBUTING.md).

## Licence

Apache License 2.0. SonarQube, SonarQube Server and SonarQube Cloud are
trademarks of SonarSource Sàrl; this plugin is an independent project, not
affiliated with or endorsed by SonarSource.
