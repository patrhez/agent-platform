#!/usr/bin/env sh

set -eu

api_url="${API_URL:-http://localhost:8080/api/v1}"
timeout_seconds="${E2E_TIMEOUT_SECONDS:-120}"
test_directory=$(mktemp -d)
stream_pid=""

cleanup() {
  if [ -n "$stream_pid" ] && kill -0 "$stream_pid" 2>/dev/null; then
    kill "$stream_pid" 2>/dev/null || true
    wait "$stream_pid" 2>/dev/null || true
  fi
  rm -rf "$test_directory"
}
trap cleanup EXIT HUP INT TERM

create_response=$(curl --noproxy '*' --fail-with-body -sS -X POST "$api_url/conversations" \
  -H 'Content-Type: application/json' \
  -d '{"content":"List the projects in the current workspace and explain the result as a Markdown list."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')
conversation_id=$(printf '%s' "$create_response" | sed -n 's/.*"conversation":{[^}]*"id":"\([^"]*\)".*/\1/p')
if [ -z "$run_id" ] || [ -z "$conversation_id" ]; then
  printf '%s\n' "create Conversation response did not contain Run and Conversation IDs" >&2
  exit 1
fi

stream_file="$test_directory/events.sse"
curl --noproxy '*' --fail-with-body -sS -N "$api_url/runs/$run_id/events?after=0" > "$stream_file" &
stream_pid=$!

deadline=$(( $(date +%s) + timeout_seconds ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if grep -q '^event: run\.completed' "$stream_file"; then
    break
  fi
  if grep -Eq '^event: run\.(failed|cancelled)' "$stream_file"; then
    printf '%s\n' "Run $run_id reached a non-success terminal event" >&2
    exit 1
  fi
  sleep 1
done

if ! grep -q '^event: run\.completed' "$stream_file"; then
  printf '%s\n' "Run $run_id did not complete within ${timeout_seconds}s" >&2
  exit 1
fi
wait "$stream_pid"
stream_pid=""

delta_line=$(awk '/^event: assistant\.delta/{want=1; next} want && /^data: /{if ($0 ~ /"text":[[:space:]]*"[^"]/) {print NR; exit} want=0}' "$stream_file")
terminal_line=$(grep -n '^event: run\.completed' "$stream_file" | head -n 1 | cut -d: -f1)
if [ -z "$delta_line" ] || [ "$delta_line" -ge "$terminal_line" ]; then
  printf '%s\n' "assistant.delta was not observed before run.completed" >&2
  exit 1
fi

awk '/^id: /{current=$2; if (seen && current <= previous) exit 1; previous=current; seen=1} END{if (!seen) exit 1}' "$stream_file"

conversation=$(curl --noproxy '*' --fail-with-body -sS "$api_url/conversations/$conversation_id")
if ! printf '%s' "$conversation" | grep -q '"role":"assistant"'; then
  printf '%s\n' "terminal Conversation does not contain an assistant Message" >&2
  exit 1
fi
trace=$(curl --noproxy '*' --fail-with-body -sS "$api_url/runs/$run_id/trace")
if ! printf '%s' "$trace" | grep -Eq '"steps":\[\{|"toolCalls":\[\{'; then
  printf '%s\n' "Run trace does not contain a persisted Step or Tool Call" >&2
  exit 1
fi

printf '%s\n' "Run $run_id streamed durable assistant Deltas and completed successfully"
