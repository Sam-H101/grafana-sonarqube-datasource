# Contributing

Bug reports and pull requests are welcome in the
[issue tracker](https://github.com/Sam-H101/grafana-sonarqube-datasource/issues).

To try the plugin, or to see what to test, read the
[testing guide](docs/testing.md).

## Building and testing

Everything runs in Docker; nothing else needs installing. The toolbox
container, the dev stack (SonarQube with sample projects, a SOCKS proxy and
Grafana) and every command are described in
[devenv/README.md](devenv/README.md). In short:

```sh
devenv/run.sh npm ci
devenv/run.sh npm run build            # frontend
devenv/run.sh mage -v build:linux      # backend
docker compose up -d --build --wait    # Grafana on http://localhost:3000
```

Before opening a pull request, run the checks CI runs:

```sh
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci
devenv/run.sh mage test
devenv/run.sh golangci-lint run ./...
devenv/run.sh go test -tags integration ./tests/integration/...
devenv/run.sh npm run e2e
```

## Layout

| Path | Contents |
| --- | --- |
| `src/` | Frontend: config editor, query editor, variable support |
| `src/query.ts`, `src/deployment.ts` | Query logic with no Grafana runtime, unit tested |
| `pkg/models/` | Settings and query validation |
| `pkg/sonar/` | SonarQube Web API client; `sonartest/` is a fake server with captured responses |
| `pkg/plugin/` | Query, health check and resource handlers; turns answers into data frames |
| `tests/e2e/`, `tests/integration/` | Playwright and Go tests against the dev stack |
| `provisioning/` | Data sources, demo dashboard and alert rule for the dev stack |
| `devenv/sonarqube/seed.py` | Creates the sample projects and their analysis history |
| `docs/sonarqube-api-notes.md` | API behaviour the backend relies on, as observed |

Files under `.config/` come from `@grafana/create-plugin`; change them with
`npx @grafana/create-plugin@latest update` rather than by hand.

## Adding a query type

1. Add it to `QueryType` in `pkg/models/query.go` and `src/types.ts`, and
   validate its fields in `ParseQuery`.
2. Add the API call to `pkg/sonar/client.go`, with a captured response in
   `pkg/sonar/sonartest/testdata/` and a route in `sonartest/fake.go`.
3. Build its frame in `pkg/plugin/query.go`.
4. Describe it in `QUERY_TYPES` in `src/query.ts`; the editor shows the fields
   that entry asks for.
5. Cover it in `pkg/plugin/plugin_test.go`, `tests/integration/` and
   `tests/e2e/queryEditor.spec.ts`.

Anything that differs between SonarQube Server and SonarQube Cloud belongs in
`pkg/sonar` and in `docs/sonarqube-api-notes.md`, with a test for each.

## Installing a build without the catalog

Unzip a release archive into Grafana's plugins directory, so that you have
`<plugins>/smartquinoa1504-sonarqube-datasource/plugin.json`. Archives from
this repository are unsigned until the plugin is approved for the Grafana
catalog, so allow it in `grafana.ini` and restart Grafana:

```ini
[plugins]
allow_loading_unsigned_plugins = smartquinoa1504-sonarqube-datasource
```

## Releasing

1. Set the version in `package.json` (and `package-lock.json`) and add a dated
   entry to `CHANGELOG.md`.
2. Push that commit, then push a tag for it, for example `v0.1.0`, or create a
   release with that tag in the GitHub UI. The Release workflow builds the
   plugin for every platform and attaches the zip and its SHA-1 to a draft
   GitHub release.

The tag must equal `v` plus the version in `package.json`: Grafana rejects a
plugin whose built version differs from the source. The workflow checks this
first and fails within seconds if they differ.

Signing is controlled by repository variables, described at the top of
`.github/workflows/release.yml`.
