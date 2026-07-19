#!/usr/bin/env sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_directory=$(mktemp -d)
trap 'rm -rf "$test_directory"' EXIT

export PATH="$repository_root/tests/scripts/fakes:$PATH"
export DOCKER_CALL_LOG="$test_directory/docker.log"
export PODMAN_CALL_LOG="$test_directory/podman.log"
export PODMAN_MACHINE_CREATED="$test_directory/podman-created"
export PODMAN_MACHINE_STATE="$test_directory/podman-running"
export GO_CALL_LOG="$test_directory/go.log"
export GO_ENV_LOG="$test_directory/go-env.log"
export NPM_CALL_LOG="$test_directory/npm.log"
export CURL_ENV_LOG="$test_directory/curl-env.log"
export LOCAL_COMMAND_LOG="$test_directory/local.log"
export LOCAL_STATE_DIR="$test_directory/state"
export ENV_FILE="$test_directory/test.env"
export REPOS_DIR="$test_directory/repos"
export MYSQL_DSN='agent:agent@tcp(127.0.0.1:3306)/agent_platform?parseTime=true&charset=utf8mb4&loc=UTC'
export REDIS_URL='redis://127.0.0.1:6379/0'
export LLM_BASE_URL='https://example.invalid'
export LLM_API_KEY='test-key'
export LLM_MODEL='test-model'
export WORKSPACE_MCP_URL='http://127.0.0.1:8081/mcp'
export WORKER_ID='worker-test'
mkdir -p "$REPOS_DIR"
: > "$ENV_FILE"

"$repository_root/scripts/bootstrap.sh" --runtime podman --env-file "$ENV_FILE" >/dev/null

grep -Fx 'machine init --cpus 4 --memory 6144 --disk-size 60' "$PODMAN_CALL_LOG" >/dev/null
grep -Fx 'machine start' "$PODMAN_CALL_LOG" >/dev/null
grep -Fx 'compose up -d mysql redis' "$PODMAN_CALL_LOG" >/dev/null
grep -Fx 'podman' "$LOCAL_STATE_DIR/container-runtime" >/dev/null
for role in mcp api worker web; do
  grep -F "start $role " "$LOCAL_COMMAND_LOG" >/dev/null
done

"$repository_root/scripts/down.sh" >/dev/null
grep -Fx 'compose down' "$PODMAN_CALL_LOG" >/dev/null

LOCAL_STATE_DIR="$test_directory/docker-state"
LOCAL_COMMAND_LOG="$test_directory/docker-local.log"
export LOCAL_STATE_DIR LOCAL_COMMAND_LOG
"$repository_root/scripts/bootstrap.sh" --env-file "$ENV_FILE" >/dev/null
grep -Fx 'compose up -d mysql redis' "$DOCKER_CALL_LOG" >/dev/null
grep -Fx 'docker' "$LOCAL_STATE_DIR/container-runtime" >/dev/null
