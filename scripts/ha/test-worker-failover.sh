#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}
API_URL=${API_URL:-http://127.0.0.1:5173/api/v1}
E2E_TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-180}

load_local_configuration
mysql_database=$(read_env MYSQL_DATABASE)
mysql_user=$(read_env MYSQL_USER)
mysql_password=$(read_env MYSQL_PASSWORD)

run_state() {
  query="SELECT CONCAT_WS('|', status, COALESCE(lease_owner, ''), attempt, "
  query="${query}(SELECT COUNT(*) FROM run_checkpoints WHERE run_id = runs.id)) "
  query="${query}FROM runs WHERE id = '$run_id';"
  docker_compose exec -T \
    --env "MYSQL_PWD=$mysql_password" \
    mysql mysql \
    --batch \
    --skip-column-names \
    --user "$mysql_user" \
    "$mysql_database" \
    --execute "$query" 2>/dev/null | tr -d '\r'
}

create_response=$(curl --noproxy '*' --fail-with-body --silent --show-error \
  --request POST "$API_URL/conversations" \
  --header 'Content-Type: application/json' \
  --data '{"content":"Inspect agent-platform files and explain Worker lease and checkpoint recovery."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')
if ! printf '%s' "$run_id" | grep -Eq '^[0-9A-HJKMNP-TV-Z]{26}$'; then
  printf '%s\n' "Conversation response did not contain a valid Run ID." >&2
  exit 1
fi

deadline=$(( $(date +%s) + 90 ))
first_worker=""
first_attempt=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  state=$(run_state)
  status=$(printf '%s' "$state" | cut -d '|' -f 1)
  owner=$(printf '%s' "$state" | cut -d '|' -f 2)
  attempt=$(printf '%s' "$state" | cut -d '|' -f 3)
  checkpoints=$(printf '%s' "$state" | cut -d '|' -f 4)
  if [ "$status" = "running" ] && [ -n "$owner" ] && [ "${checkpoints:-0}" -gt 0 ]; then
    first_worker=$owner
    first_attempt=$attempt
    break
  fi
  if [ "$status" = "succeeded" ] || [ "$status" = "failed" ] || [ "$status" = "cancelled" ]; then
    printf '%s\n' "Run $run_id finished before a checkpointed Worker failure could be injected; rerun the test." >&2
    exit 1
  fi
  sleep 1
done
if [ -z "$first_worker" ]; then
  printf '%s\n' "Run $run_id did not reach a checkpointed running state." >&2
  exit 1
fi
if ! kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  get pod "$first_worker" >/dev/null 2>&1; then
  printf '%s\n' \
    "Run $run_id was claimed by non-cluster Worker $first_worker." \
    "Stop host/full-container Workers, then rerun ./scripts/ha/up.sh before this test." >&2
  exit 1
fi

printf '%s\n' "Run $run_id is owned by $first_worker at attempt $first_attempt; terminating that Worker Pod."
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  delete pod "$first_worker" --grace-period=0 --force --wait=false

deadline=$(( $(date +%s) + E2E_TIMEOUT_SECONDS ))
replacement_worker=""
replacement_attempt=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  state=$(run_state)
  status=$(printf '%s' "$state" | cut -d '|' -f 1)
  owner=$(printf '%s' "$state" | cut -d '|' -f 2)
  attempt=$(printf '%s' "$state" | cut -d '|' -f 3)
  if [ -z "$replacement_worker" ] && [ "$owner" != "$first_worker" ] && [ "$attempt" -gt "$first_attempt" ]; then
    replacement_worker=$owner
    replacement_attempt=$attempt
    printf '%s\n' "Run was reclaimed by $replacement_worker at attempt $replacement_attempt."
  fi
  case "$status" in
    succeeded)
      if [ -z "$replacement_worker" ]; then
        printf '%s\n' "Run succeeded without recording a replacement Worker claim." >&2
        exit 1
      fi
      printf '%s\n' \
        "PASS: Worker failover recovered checkpointed Run $run_id." \
        "  Before: $first_worker (attempt $first_attempt)" \
        "  After:  $replacement_worker (attempt $replacement_attempt)"
      exit 0
      ;;
    failed|cancelled)
      printf '%s\n' "Run $run_id reached unexpected terminal status: $status" >&2
      exit 1
      ;;
  esac
  sleep 2
done

printf '%s\n' "Run $run_id did not recover within ${E2E_TIMEOUT_SECONDS}s." >&2
exit 1
