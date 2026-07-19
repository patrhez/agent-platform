#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

HA_CLUSTER=${HA_CLUSTER:-agent-platform-ha}
HA_STATE_DIR="$LOCAL_STATE_DIR/ha"

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  printf '%s\n' \
    "Usage: ./scripts/ha/down.sh [--dependencies]" \
    "Deletes the k3d lab and dashboard. MySQL/Redis remain running unless --dependencies is supplied."
  exit 0
fi
if [ "$#" -gt 1 ] || { [ "$#" -eq 1 ] && [ "$1" != "--dependencies" ]; }; then
  printf '%s\n' "Usage: ./scripts/ha/down.sh [--dependencies]" >&2
  exit 2
fi

dashboard_pid_file="$HA_STATE_DIR/dashboard.pid"
if [ -f "$dashboard_pid_file" ]; then
  dashboard_pid=$(sed -n '1p' "$dashboard_pid_file")
  if [ -n "$dashboard_pid" ] && kill -0 "$dashboard_pid" 2>/dev/null; then
    kill "$dashboard_pid"
  fi
  rm -f "$dashboard_pid_file"
fi

if command -v k3d >/dev/null 2>&1; then
  k3d cluster delete "$HA_CLUSTER" 2>/dev/null || true
fi

if [ "${1:-}" = "--dependencies" ]; then
  cd "$REPOSITORY_ROOT"
  docker_compose down
fi

printf '%s\n' "Agent Platform HA lab stopped."
