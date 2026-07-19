#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

role=${1:-}
case "$role" in api|worker|mcp|web) ;; *) printf '%s\n' "Usage: ./scripts/restart.sh {api|worker|mcp|web}" >&2; exit 2 ;; esac
load_local_configuration
stop_role "$role"
agent_config="$REPOSITORY_ROOT/backend/configs/agents/issue-troubleshooter.yaml"
case "$role" in
  mcp) start_role mcp env WORKSPACE_ROOT="$REPOS_DIR" "$LOCAL_BIN_DIR/workspace-mcp"; wait_for_url "Workspace MCP" "http://127.0.0.1:8081/healthz" ;;
  api) start_role api env -u REPOS_DIR AGENT_CONFIG_PATH="$agent_config" "$LOCAL_BIN_DIR/api"; wait_for_url "API Server" "http://127.0.0.1:8080/healthz" ;;
  worker) start_role worker env -u REPOS_DIR AGENT_CONFIG_PATH="$agent_config" "$LOCAL_BIN_DIR/worker" ;;
  web) start_role web env -u REPOS_DIR npm --prefix "$REPOSITORY_ROOT/frontend" run dev -- --host 127.0.0.1 ;;
esac
