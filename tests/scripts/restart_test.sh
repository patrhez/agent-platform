#!/usr/bin/env sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_directory=$(mktemp -d)
trap 'rm -rf "$test_directory"' EXIT

export PATH="$repository_root/tests/scripts/fakes:$PATH"
export LOCAL_COMMAND_LOG="$test_directory/local.log"
export LOCAL_STATE_DIR="$test_directory/state"
export REPOS_DIR="$test_directory/repos"
export MYSQL_DSN='test-dsn'
export REDIS_URL='redis://127.0.0.1:6379/0'
export LLM_BASE_URL='https://example.invalid'
export LLM_API_KEY='test-key'
export LLM_MODEL='test-model'
export WORKSPACE_MCP_URL='http://127.0.0.1:8081/mcp'
export WORKER_ID='worker-test'
mkdir -p "$REPOS_DIR" "$LOCAL_STATE_DIR/bin"
printf '#!/usr/bin/env sh\nexit 0\n' > "$LOCAL_STATE_DIR/bin/worker"
chmod +x "$LOCAL_STATE_DIR/bin/worker"

"$repository_root/scripts/restart.sh" worker >/dev/null

grep -Fx 'stop worker' "$LOCAL_COMMAND_LOG" >/dev/null
grep -F 'start worker ' "$LOCAL_COMMAND_LOG" >/dev/null
