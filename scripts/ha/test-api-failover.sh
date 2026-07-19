#!/usr/bin/env sh

set -eu

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}
API_URL=${API_URL:-http://127.0.0.1:5173/api/v1}
E2E_TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-180}
HA_PORT_FORWARD_PORT=${HA_PORT_FORWARD_PORT:-18080}

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/agent-platform-api-ha.XXXXXX")
stream_pid=""
port_forward_pid=""

cleanup() {
  if [ -n "$stream_pid" ] && kill -0 "$stream_pid" 2>/dev/null; then
    kill "$stream_pid" 2>/dev/null || true
  fi
  if [ -n "$port_forward_pid" ] && kill -0 "$port_forward_pid" 2>/dev/null; then
    kill "$port_forward_pid" 2>/dev/null || true
  fi
  rm -rf "$temporary_directory"
}
trap cleanup EXIT HUP INT TERM

wait_for_file_value() {
  file=$1
  pattern=$2
  deadline=$(( $(date +%s) + 30 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if [ -f "$file" ] && grep -q "$pattern" "$file"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

create_response=$(curl --noproxy '*' --fail-with-body --silent --show-error \
  --request POST "$API_URL/conversations" \
  --header 'Content-Type: application/json' \
  --data '{"content":"List the project names in the current workspace. Be concise."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')
if ! printf '%s' "$run_id" | grep -Eq '^[0-9A-HJKMNP-TV-Z]{26}$'; then
  printf '%s\n' "Conversation response did not contain a valid Run ID." >&2
  exit 1
fi

initial_api_pods=$(kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  get pods \
  --selector app.kubernetes.io/component=api \
  --output jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
first_instance=$(printf '%s\n' "$initial_api_pods" | head -n 1)
if [ -z "$first_instance" ]; then
  printf '%s\n' "No API Pod is available for the HA test." >&2
  exit 1
fi

kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  port-forward "pod/$first_instance" "$HA_PORT_FORWARD_PORT:8080" \
  > "$temporary_directory/port-forward.log" 2>&1 &
port_forward_pid=$!
deadline=$(( $(date +%s) + 30 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if curl --noproxy '*' --fail --silent \
    "http://127.0.0.1:$HA_PORT_FORWARD_PORT/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! kill -0 "$port_forward_pid" 2>/dev/null; then
  printf '%s\n' "Could not connect directly to API Pod $first_instance." >&2
  exit 1
fi

first_events="$temporary_directory/first.events"
curl --noproxy '*' --silent --show-error --no-buffer \
  --max-time "$E2E_TIMEOUT_SECONDS" \
  --output "$first_events" \
  "http://127.0.0.1:$HA_PORT_FORWARD_PORT/api/v1/runs/$run_id/events?after=0" &
stream_pid=$!

if ! wait_for_file_value "$first_events" '^id: '; then
  printf '%s\n' "Run $run_id did not emit an SSE event." >&2
  exit 1
fi

printf '%s\n' "SSE initially connected to API Pod: $first_instance"
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  delete pod "$first_instance" --grace-period=0 --force --wait=false
wait "$stream_pid" 2>/dev/null || true
stream_pid=""
if kill -0 "$port_forward_pid" 2>/dev/null; then
  kill "$port_forward_pid" 2>/dev/null || true
fi
wait "$port_forward_pid" 2>/dev/null || true
port_forward_pid=""

last_event_id=$(sed -n 's/^id: \([0-9][0-9]*\).*/\1/p' "$first_events" | tail -n 1)
if [ -z "$last_event_id" ]; then
  printf '%s\n' "No durable SSE cursor was captured before API termination." >&2
  exit 1
fi

second_events="$temporary_directory/second.events"
curl --noproxy '*' --fail-with-body --silent --show-error --no-buffer \
  --max-time "$E2E_TIMEOUT_SECONDS" \
  --header "Last-Event-ID: $last_event_id" \
  --output "$second_events" \
  "$API_URL/runs/$run_id/events?after=0"

first_replayed_id=$(sed -n 's/^id: \([0-9][0-9]*\).*/\1/p' "$second_events" | head -n 1)
if [ -z "$first_replayed_id" ] || [ "$first_replayed_id" -le "$last_event_id" ]; then
  printf '%s\n' "SSE replay did not resume strictly after event $last_event_id." >&2
  exit 1
fi

deadline=$(( $(date +%s) + 60 ))
replacement_instance=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  ready_api_pods=$(kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
    get pods \
    --selector app.kubernetes.io/component=api \
    --field-selector status.phase=Running \
    --output jsonpath='{range .items[?(@.status.containerStatuses[0].ready==true)]}{.metadata.name}{"\n"}{end}')
  for pod_name in $ready_api_pods; do
    if ! printf '%s\n' "$initial_api_pods" | grep -qx "$pod_name"; then
      replacement_instance=$pod_name
    fi
  done
  if [ "$(printf '%s\n' "$ready_api_pods" | sed '/^$/d' | wc -l | tr -d ' ')" -eq 3 ] && \
    [ -n "$replacement_instance" ]; then
    break
  fi
  sleep 1
done
if [ -z "$replacement_instance" ]; then
  printf '%s\n' "Kubernetes did not create a Ready replacement API Pod." >&2
  exit 1
fi

expected=$((last_event_id + 1))
if ! sed -n 's/^id: \([0-9][0-9]*\).*/\1/p' "$second_events" |
  awk -v expected="$expected" '{ if ($1 != expected) exit 1; expected++ }'; then
  printf '%s\n' "Recovered SSE events contain a sequence gap." >&2
  exit 1
fi

run_response=$(curl --noproxy '*' --fail-with-body --silent --show-error "$API_URL/runs/$run_id")
run_status=$(printf '%s' "$run_response" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
if [ "$run_status" != "succeeded" ]; then
  printf '%s\n' "Run $run_id ended with status $run_status instead of succeeded." >&2
  exit 1
fi

printf '%s\n' \
  "PASS: API failover recovered Run $run_id." \
  "  Before: $first_instance" \
  "  Replacement: $replacement_instance" \
  "  Cursor: $last_event_id -> $first_replayed_id, with no sequence gaps"
