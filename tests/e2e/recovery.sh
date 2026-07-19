#!/usr/bin/env sh

set -eu

api_url="${API_URL:-http://localhost:8080/api/v1}"
timeout_seconds="${E2E_TIMEOUT_SECONDS:-120}"

create_response=$(curl --noproxy '*' --fail-with-body -sS -X POST "$api_url/conversations" -H 'Content-Type: application/json' -d '{"content":"In the agent-platform repository, locate the configured HTTP server entry point."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')

if [ -z "$run_id" ]; then
  printf '%s\n' "create Conversation response did not contain a runId" >&2
  exit 1
fi

./scripts/restart.sh worker

deadline=$(( $(date +%s) + timeout_seconds ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  response=$(curl --noproxy '*' --fail-with-body -sS "$api_url/runs/$run_id")
  status=$(printf '%s' "$response" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
  case "$status" in
    succeeded)
      printf '%s\n' "Run $run_id recovered and reached terminal status: succeeded"
      exit 0
      ;;
    failed|cancelled)
      printf '%s\n' "Run $run_id reached unexpected terminal status: $status" >&2
      exit 1
      ;;
  esac
  sleep 2
done

printf '%s\n' "Run $run_id did not reach a terminal state within ${timeout_seconds}s" >&2
exit 1
