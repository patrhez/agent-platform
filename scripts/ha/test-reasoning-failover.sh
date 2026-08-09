#!/usr/bin/env sh

# Verifies the durable boundary between a reasoning-model tool call and its
# result. "baseline" removes the encrypted replay envelope to verify fail-closed
# recovery; "protected" proves a replacement Worker can continue.
set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}
API_URL=${API_URL:-http://127.0.0.1:5173/api/v1}
MODE=${MODE:-protected}
E2E_TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-180}
CHECKPOINT_PAUSE=${CHECKPOINT_PAUSE:-10s}

case "$MODE" in baseline|protected) ;; *) printf '%s\n' 'MODE must be baseline or protected.' >&2; exit 2;; esac

load_local_configuration
if [ -z "${REPLAY_ENCRYPTION_KEY:-}" ] &&
  { [ -z "${REPLAY_ENCRYPTION_ACTIVE_KEY_ID:-}" ] || [ -z "${REPLAY_ENCRYPTION_KEYS:-}" ]; }; then
  printf '%s\n' 'A replay encryption key or keyring is required.' >&2
  exit 2
fi

mysql_database=$(read_env MYSQL_DATABASE)
mysql_user=$(read_env MYSQL_USER)
mysql_password=$(read_env MYSQL_PASSWORD)

cleanup() {
  kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
    set env deployment/worker HA_TEST_TOOL_CALL_CHECKPOINT_DELAY- \
    HA_TEST_REQUIRE_REASONING_REPLAY- >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

mysql_query() {
  docker_compose exec -T --env "MYSQL_PWD=$mysql_password" mysql mysql \
    --batch --skip-column-names --user "$mysql_user" "$mysql_database" --execute "$1" 2>/dev/null | tr -d '\r'
}

kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  set env deployment/worker "HA_TEST_TOOL_CALL_CHECKPOINT_DELAY=$CHECKPOINT_PAUSE" \
  HA_TEST_REQUIRE_REASONING_REPLAY=1
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  rollout status deployment/worker --timeout=90s

create_response=$(curl --noproxy '*' --fail-with-body --silent --show-error \
  --request POST "$API_URL/conversations" --header 'Content-Type: application/json' \
  --data '{"content":"Immediately call workspace.list_repositories. After its result, reply only: OK."}')
run_id=$(printf '%s' "$create_response" | sed -n 's/.*"runId":"\([^"]*\)".*/\1/p')
if ! printf '%s' "$run_id" | grep -Eq '^[0-9A-HJKMNP-TV-Z]{26}$'; then
  printf '%s\n' 'Conversation response did not contain a valid Run ID.' >&2
  exit 1
fi

deadline=$(( $(date +%s) + 90 ))
checkpoint_ordinal=''
first_worker=''
while [ "$(date +%s)" -lt "$deadline" ]; do
  checkpoint=$(mysql_query "
    SELECT CONCAT(c.ordinal, '|', COUNT(r.checkpoint_id))
    FROM run_checkpoints c
    LEFT JOIN run_checkpoint_replays r ON r.checkpoint_id = c.id
    WHERE c.run_id = '$run_id'
      AND JSON_LENGTH(JSON_EXTRACT(c.payload, '$.messages[last].tool_calls')) > 0
      AND c.payload LIKE '%workspace_list_repositories%'
    GROUP BY c.id, c.ordinal
    ORDER BY c.ordinal DESC
    LIMIT 1;")
  state=$(mysql_query "
    SELECT CONCAT_WS('|', status, COALESCE(lease_owner, ''), attempt)
    FROM runs
    WHERE id = '$run_id';")
  if [ -n "$checkpoint" ] && [ -n "$state" ]; then
    checkpoint_ordinal=$(printf '%s' "$checkpoint" | cut -d '|' -f 1)
    first_worker=$(printf '%s' "$state" | cut -d '|' -f 2)
    replay_count=$(printf '%s' "$checkpoint" | cut -d '|' -f 2)
    break
  fi
  sleep 1
done
if [ -z "$first_worker" ]; then
  printf '%s\n' "Run $run_id did not reach the required reasoning tool-call checkpoint." >&2
  exit 1
fi
if [ "$replay_count" != 1 ]; then
  printf '%s\n' "Run $run_id reached a tool-call checkpoint without its replay companion." >&2
  exit 1
fi

if [ "$MODE" = baseline ]; then
  mysql_query "
    DELETE r
    FROM run_checkpoint_replays r
    JOIN run_checkpoints c ON c.id = r.checkpoint_id
    WHERE c.run_id = '$run_id'
      AND c.ordinal = $checkpoint_ordinal;" >/dev/null
  printf '%s\n' "Baseline mode removed the replay companion for checkpoint ordinal $checkpoint_ordinal."
fi

printf '%s\n' "Run $run_id reached checkpoint $checkpoint_ordinal on $first_worker; force-deleting its Worker Pod."
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  delete pod "$first_worker" --grace-period=0 --force --wait=false

deadline=$(( $(date +%s) + E2E_TIMEOUT_SECONDS ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  result=$(mysql_query "
    SELECT CONCAT_WS('|', status, attempt, COALESCE(terminal_error_code, ''))
    FROM runs
    WHERE id = '$run_id';")
  status=$(printf '%s' "$result" | cut -d '|' -f 1)
  attempt=$(printf '%s' "$result" | cut -d '|' -f 2)
  error_code=$(printf '%s' "$result" | cut -d '|' -f 3)
  case "$status" in
    succeeded)
      if [ "$MODE" = protected ] && [ "$attempt" -gt 1 ]; then
        printf '%s\n' "PASS: protected replay survived Worker failover for Run $run_id (attempt $attempt)."
        exit 0
      fi
      printf '%s\n' "Unexpected success for MODE=$MODE (attempt $attempt)." >&2; exit 1;;
    failed|cancelled)
      if [ "$MODE" = baseline ] && [ "$attempt" -gt 1 ] && [ "$error_code" = provider_replay_missing ]; then
        printf '%s\n' "PASS: missing replay failed closed for Run $run_id (attempt $attempt)."
        exit 0
      fi
      printf '%s\n' "Unexpected terminal state: $result" >&2; exit 1;;
  esac
  sleep 2
done

printf '%s\n' "Run $run_id did not complete within ${E2E_TIMEOUT_SECONDS}s." >&2
exit 1
