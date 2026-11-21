# Development environment

Everything runs in Docker: you need Docker with Compose v2 and a Bash shell
(Linux, macOS, or WSL on Windows). Node, Go and the other build tools live in a
toolbox container, so nothing else has to be installed. Run the commands below
from the repository root.

## Toolbox

`devenv/toolbox/Dockerfile` has Node, Go, Mage, golangci-lint and Chromium's
system libraries. `node_modules`, Go caches, the npm cache and Playwright
browsers live in Docker volumes; only build output (`dist/`) is written to the
working tree. `devenv/run.sh` runs a command in the toolbox as your own user, so
files it writes belong to you.

```sh
devenv/run.sh npm ci                 # install frontend deps (into a volume)
devenv/run.sh npm run build          # frontend
devenv/run.sh npm run dev            # frontend, rebuild on change
devenv/run.sh mage -v build:linux    # backend for the dev stack
devenv/run.sh mage -v buildAll       # backend for every platform
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci        # frontend unit tests
devenv/run.sh mage test              # backend unit tests
devenv/run.sh golangci-lint run ./...
devenv/run.sh                        # shell
```

## Dev stack

Build the plugin into `dist/` first, then:

```sh
docker compose up -d --build --wait  # SonarQube, its seed, the proxy, Grafana
docker compose restart grafana       # pick up a rebuilt backend binary
docker compose logs -f grafana
docker compose down                  # add -v to drop volumes too
```

| Service | Address | Purpose |
| --- | --- | --- |
| Grafana | http://localhost:3000 | Provisioned data sources, demo dashboard and alert rule |
| `sonarqube` | http://localhost:9000 | SonarQube Community Build (admin / admin) |
| `seed` | runs once | Creates three sample projects with backdated analyses, and a token |
| `socks` | internal | A SOCKS5 proxy, standing in for Private data source connect |

The first start takes about three minutes, most of it the seed analysing the
sample projects. The seed is idempotent: later starts find the projects and
keep the token.

SonarQube needs about 2.5 GB of memory. Its Elasticsearch production checks
are turned off, so `vm.max_map_count` does not have to be raised.

Inside Docker, Grafana reaches SonarQube as `http://sonarqube:9000`. The seed
writes the token to the `sonar-seed` volume, where the provisioned data sources
and the toolbox read it. To print it:

```sh
docker compose run --rm --no-deps -T --entrypoint cat seed /seed-out/token
```

All ports bind to 127.0.0.1. The dev Grafana grants anonymous Admin; see
[Exposing the stack](#exposing-the-stack-on-the-network) before opening it up.

The versions tested are pinned in `docker-compose.yaml`. Override them with
`GRAFANA_VERSION`, `GRAFANA_IMAGE` (`grafana` or `grafana-enterprise`),
`SONARQUBE_IMAGE` and `SCANNER_IMAGE`. `GRAFANA_PORT` and `SONARQUBE_PORT`
change the host ports. For example, to test against the 9.9 long-term release
next to the normal stack:

```sh
export SONARQUBE_IMAGE=sonarqube:9.9-community SONARQUBE_PORT=9020 GRAFANA_PORT=3002
docker compose -p sq99 up -d --wait sonarqube && docker compose -p sq99 up seed
docker compose -p sq99 run --rm toolbox go test -tags integration ./tests/integration/...
docker compose -p sq99 down -v
```

### The private network

`sonarqube` is also on a second Docker network, `private`, under the name
`sonarqube-private`. Grafana is not on that network, so it cannot resolve the
name; the `socks` proxy is on both. Grafana is started with the secure socks
proxy pointing at it (`GF_SECURE_SOCKS_DATASOURCE_PROXY_*` in
`docker-compose.yaml`, with `allow_insecure` because a plain SOCKS proxy has no
mutual TLS). A data source with the secure socks proxy turned on therefore
reaches `http://sonarqube-private:9000`, and one without it does not. That is
how the tests check the Private data source connect path.

### Sample data

`devenv/sonarqube/seed.py` generates small Python sources with a known number
of bugs, vulnerabilities, code smells and covered lines, and analyses them
with `sonar.projectDate` set in the past:

| Project | Analyses | Story |
| --- | --- | --- |
| Shop API (`demo:shop-api`) | 6, from 84 days ago | Coverage rises, bugs fall, one failed quality gate |
| Shop Web (`demo:shop-web`) | 4, from 70 days ago | Slowly getting worse |
| Legacy Billing (`demo:legacy-billing`) | 2, from 56 days ago | Low coverage, many issues |

To start again from nothing: `docker compose down -v`.

## Tests against the stack

```sh
devenv/run.sh npx playwright install chromium   # once, into the volume
devenv/run.sh npm run e2e                       # Playwright, tests/e2e
devenv/run.sh go test -tags integration ./tests/integration/...
```

Inside the toolbox these reach the compose services by name, and the token is
mounted at `/run/sonar/token`. To run them from elsewhere (as CI does), set
`SONAR_URL` and `SONAR_TOKEN_FILE` (or `SONAR_TOKEN`).

The tests that use sonarcloud.io are skipped unless `SONARCLOUD_TESTS=1`:

```sh
SONARCLOUD_TESTS=1 devenv/run.sh npm run e2e
SONARCLOUD_TESTS=1 devenv/run.sh go test -tags integration ./tests/integration/...
```

Playwright saves a screenshot of each step under `artifacts/app-tests/`, so you
can check what the tests saw. To regenerate the screenshots shipped with the
plugin:

```sh
devenv/run.sh bash -c 'DOCS_SCREENSHOTS=1 npx playwright test tests/e2e/docs-screenshots.spec.ts'
```

## Exposing the stack on the network

To reach the stack from other machines, turn off anonymous Admin and set an
admin password. The anonymous setting is built into the image, so rebuild:

```sh
BIND_ADDRESS=0.0.0.0 ANONYMOUS_AUTH_ENABLED=false \
GRAFANA_ADMIN_PASSWORD='<password>' docker compose up -d --build --wait
```

SonarQube's admin password is still `admin`; change it in SonarQube before
exposing port 9000. The e2e tests log in to Grafana as admin/admin, so run them
against the default local stack.
