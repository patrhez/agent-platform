#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

usage() {
  printf '%s\n' \
    "Usage: ./scripts/up.sh [--runtime docker|podman]" \
    "Starts MySQL/Redis in the selected runtime and application roles on the host."
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

require_command "$CONTAINER_RUNTIME" "Run ./scripts/bootstrap.sh --runtime $CONTAINER_RUNTIME first."
require_command go "Install Go 1.25 or newer."
require_command node "Install Node.js."
require_command npm "Install npm."
require_command curl "Install curl."
"$CONTAINER_RUNTIME" compose version >/dev/null
"$CONTAINER_RUNTIME" info >/dev/null
load_local_configuration

cleanup_failed_start() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    for role in $STARTED_ROLES; do stop_role "$role"; done
  fi
  exit "$status"
}
trap cleanup_failed_start EXIT HUP INT TERM

cd "$REPOSITORY_ROOT"
container_compose up -d mysql redis
wait_for_compose_service mysql
wait_for_compose_service redis
run_go_in_module backend mod download
run_go_in_module mcp-server mod download
run_go_in_module backend build -o "$LOCAL_BIN_DIR/migrate" ./cmd/migrate
run_go_in_module backend build -o "$LOCAL_BIN_DIR/api" ./cmd/api
run_go_in_module backend build -o "$LOCAL_BIN_DIR/worker" ./cmd/worker
run_go_in_module mcp-server build -o "$LOCAL_BIN_DIR/workspace-mcp" ./cmd/workspace-mcp
npm --prefix frontend ci
"$LOCAL_BIN_DIR/migrate"

agent_config="$REPOSITORY_ROOT/backend/configs/agents/issue-troubleshooter.yaml"
start_role mcp env WORKSPACE_ROOT="$REPOS_DIR" "$LOCAL_BIN_DIR/workspace-mcp"
wait_for_url "Workspace MCP" "http://127.0.0.1:8081/healthz"
start_role api env -u REPOS_DIR AGENT_CONFIG_PATH="$agent_config" "$LOCAL_BIN_DIR/api"
wait_for_url "API Server" "http://127.0.0.1:8080/healthz"
start_role worker env -u REPOS_DIR AGENT_CONFIG_PATH="$agent_config" "$LOCAL_BIN_DIR/worker"
start_role web env -u REPOS_DIR npm --prefix "$REPOSITORY_ROOT/frontend" run dev -- --host 127.0.0.1

printf '%s\n' "Agent Platform is running:" \
  "  Web: http://127.0.0.1:5173" \
  "  API: http://127.0.0.1:8080" \
  "  MCP: http://127.0.0.1:8081/mcp" \
  "  Logs: $LOCAL_LOG_DIR" \
  "Keep this terminal open. Press Ctrl-C to stop host services."
supervise_roles mcp api worker web
trap - EXIT HUP INT TERM
