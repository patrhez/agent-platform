#!/usr/bin/env sh

set -eu

api_url="${API_URL:-http://localhost:8080/api/v1}"
timeout_seconds="${E2E_TIMEOUT_SECONDS:-120}"

create_response=$(curl --noproxy '*' --fail-with-body -sS -X POST "$api_url/conversations" -H 'Content-Type: application/json' -d '{"content":"Find the workspace MCP health endpoint."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')

if [ -z "$run_id" ]; then
  printf '%s\n' "create Conversation response did not contain a runId" >&2
  exit 1
fi

first_id=$(curl --noproxy '*' --fail-with-body -sS -N "$api_url/runs/$run_id/events?after=0" | sed -n 's/^id: //p' | head -n 1)
if [ -z "$first_id" ]; then
  printf '%s\n' "Run $run_id did not produce an initial SSE event" >&2
  exit 1
fi

./scripts/restart.sh api
deadline=$(( $(date +%s) + timeout_seconds ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  stream=$(curl --noproxy '*' --fail-with-body -sS --max-time 20 \
    -H "Last-Event-ID: $first_id" "$api_url/runs/$run_id/events?after=0" || true)
  replayed_ids=$(printf '%s' "$stream" | sed -n 's/^id: //p')
  if [ -n "$replayed_ids" ] && printf '%s\n' "$replayed_ids" | awk -v cursor="$first_id" '{if ($1 <= cursor) exit 1}'; then
    printf '%s\n' "Run $run_id resumed strictly after SSE event $first_id following API restart"
    exit 0
  fi
  sleep 2
done

printf '%s\n' "Run $run_id did not replay an SSE event within ${timeout_seconds}s" >&2
exit 1
