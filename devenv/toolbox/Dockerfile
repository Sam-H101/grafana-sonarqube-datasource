# Toolbox for building and testing the plugin. Everything (Node, Go, caches,
# node_modules, browsers) lives in this image or in Docker volumes, never on the
# host. See devenv/README.md.

# Go: match the version CI uses (go.mod's `go` line), latest patch.
FROM golang:1.26-bookworm AS go

FROM node:24-bookworm

COPY --from=go /usr/local/go /usr/local/go

ARG MAGE_VERSION=v1.17.2
ARG GOLANGCI_LINT_VERSION=v2.15.0
ARG PLAYWRIGHT_VERSION=1.65.1
ARG UID=1000
ARG GID=1000

ENV GOPATH=/go \
    GOTOOLCHAIN=local \
    PATH=/usr/local/go/bin:/go/bin:$PATH \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    NPM_CONFIG_UPDATE_NOTIFIER=false

RUN GOBIN=/usr/local/bin go install github.com/magefile/mage@${MAGE_VERSION} \
 && GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} \
 && rm -rf /root/go /root/.cache

# System libraries Chromium needs. Browsers themselves are downloaded into the
# ms-playwright volume by the matching @playwright/test version at run time.
RUN npx -y playwright@${PLAYWRIGHT_VERSION} install-deps chromium \
 && apt-get install -y --no-install-recommends jq \
 && rm -rf /var/lib/apt/lists/* /root/.npm

# Run as the calling user's uid/gid (devenv/run.sh passes them) so files
# written to the bind mount belong to them.
# The node image already has uid 1000; free the requested ids if taken.
RUN if getent passwd ${UID} >/dev/null; then userdel -r "$(getent passwd ${UID} | cut -d: -f1)"; fi \
 && if ! getent group ${GID} >/dev/null; then groupadd -g ${GID} dev; fi \
 && useradd -m -u ${UID} -g ${GID} -s /bin/bash dev \
 && mkdir -p /workspace/node_modules /go/pkg/mod /home/dev/.cache/go-build /home/dev/.npm /ms-playwright \
 && chown -R ${UID}:${GID} /workspace /go /home/dev /ms-playwright

USER dev
WORKDIR /workspace
