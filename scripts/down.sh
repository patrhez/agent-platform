#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

usage() {
  printf '%s\n' "Usage: ./scripts/down.sh [--runtime docker|podman]"
}

requested_runtime=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --runtime)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      requested_runtime=$2
      shift 2
      ;;
    --runtime=*) requested_runtime=${1#*=}; shift ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
select_container_runtime "$requested_runtime"

for role in web worker api mcp; do stop_role "$role"; done
cd "$REPOSITORY_ROOT"
container_compose down
