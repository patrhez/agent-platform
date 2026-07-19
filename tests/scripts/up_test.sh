#!/usr/bin/env sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_directory=$(mktemp -d)
trap 'rm -rf "$test_directory"' EXIT

export PATH="$repository_root/tests/scripts/fakes:$PATH"
export DOCKER_CALL_LOG="$test_directory/docker.log"
export GO_CALL_LOG="$test_directory/go.log"
export GO_ENV_LOG="$test_directory/go-env.log"
export NPM_CALL_LOG="$test_directory/npm.log"
export CURL_ENV_LOG="$test_directory/curl-env.log"
export LOCAL_COMMAND_LOG="$test_directory/local.log"
export LOCAL_STATE_DIR="$test_directory/state"
export ENV_FILE="$test_directory/missing.env"
export REPOS_DIR="$test_directory/repos"
export MYSQL_DSN='agent:agent@tcp(127.0.0.1:3306)/agent_platform?parseTime=true&charset=utf8mb4&loc=UTC'
export REDIS_URL='redis://127.0.0.1:6379/0'
export LLM_BASE_URL='https://example.invalid'
export LLM_API_KEY='test-key'
export LLM_MODEL='test-model'
export WORKSPACE_MCP_URL='http://127.0.0.1:8081/mcp'
export WORKER_ID='worker-test'
mkdir -p "$REPOS_DIR"

node -e "const scripts = require('$repository_root/frontend/package.json').scripts; if (!scripts.dev) process.exit(1)"
"$repository_root/scripts/up.sh" >/dev/null

grep -Fx 'compose up -d mysql redis' "$DOCKER_CALL_LOG" >/dev/null
grep -E '(^|,)127\.0\.0\.1(,|\|)' "$CURL_ENV_LOG" >/dev/null
grep -E '(^|,)localhost(,|\|)' "$CURL_ENV_LOG" >/dev/null
grep -F "$repository_root/backend/.gomodcache|$repository_root/backend/.gocache|-C backend mod download" "$GO_ENV_LOG" >/dev/null
grep -F "$repository_root/mcp-server/.gomodcache|$repository_root/mcp-server/.gocache|-C mcp-server mod download" "$GO_ENV_LOG" >/dev/null
grep -F 'build -o ' "$GO_CALL_LOG" | grep -F '/api ./cmd/api' >/dev/null
grep -F 'build -o ' "$GO_CALL_LOG" | grep -F '/worker ./cmd/worker' >/dev/null
grep -F 'build -o ' "$GO_CALL_LOG" | grep -F '/workspace-mcp ./cmd/workspace-mcp' >/dev/null
grep -Fx -- '--prefix frontend ci' "$NPM_CALL_LOG" >/dev/null
for role in mcp api worker web; do
  grep -F "start $role " "$LOCAL_COMMAND_LOG" >/dev/null
  test -f "$LOCAL_STATE_DIR/run/$role.pid"
done
grep -F 'start api env -u REPOS_DIR ' "$LOCAL_COMMAND_LOG" >/dev/null
grep -F 'start worker env -u REPOS_DIR ' "$LOCAL_COMMAND_LOG" >/dev/null
grep -F 'start web env -u REPOS_DIR ' "$LOCAL_COMMAND_LOG" >/dev/null
grep -Fx 'supervise mcp api worker web' "$LOCAL_COMMAND_LOG" >/dev/null
