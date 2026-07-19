#!/usr/bin/env sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_directory=$(mktemp -d)
trap 'rm -rf "$test_directory"' EXIT

export PATH="$repository_root/tests/scripts/fakes:$PATH"
export DOCKER_CALL_LOG="$test_directory/docker.log"
export LOCAL_COMMAND_LOG="$test_directory/local.log"
export LOCAL_STATE_DIR="$test_directory/state"
export ENV_FILE="$test_directory/missing.env"
mkdir -p "$LOCAL_STATE_DIR/run"
for role in web worker api mcp; do printf '%s\n' 99999 > "$LOCAL_STATE_DIR/run/$role.pid"; done

"$repository_root/scripts/down.sh" >/dev/null
"$repository_root/scripts/down.sh" >/dev/null

grep -Fx 'compose down' "$DOCKER_CALL_LOG" >/dev/null
for role in web worker api mcp; do grep -Fx "stop $role" "$LOCAL_COMMAND_LOG" >/dev/null; done
