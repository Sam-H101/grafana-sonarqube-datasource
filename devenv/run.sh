#!/usr/bin/env bash
# Run a command in the toolbox container from the repo root, as the calling user.
#   devenv/run.sh npm run build
#   devenv/run.sh mage -v build:linux
#   devenv/run.sh            (interactive shell)
set -euo pipefail
cd "$(dirname "$0")/.."
export TOOLBOX_UID="$(id -u)" TOOLBOX_GID="$(id -g)"
tty_flag=()
[ -t 0 ] || tty_flag=(-T)
if [ $# -eq 0 ]; then set -- bash; fi
exec docker compose run --rm "${tty_flag[@]}" toolbox "$@"
