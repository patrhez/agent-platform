#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

HA_CLUSTER=${HA_CLUSTER:-agent-platform-ha}
HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-$HA_CLUSTER}
HA_API_PORT=${HA_API_PORT:-6550}
HA_STATE_DIR="$LOCAL_STATE_DIR/ha"
HA_DASHBOARD_ADDR=${HA_DASHBOARD_ADDR:-127.0.0.1:8090}

usage() {
  printf '%s\n' \
    "Usage: ./scripts/ha/up.sh" \
    "Builds images, starts a two-node k3d lab, deploys the platform, and starts the HA dashboard."
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi
if [ "$#" -ne 0 ]; then
  usage >&2
  exit 2
fi

install_k3d_on_macos() {
  if command -v k3d >/dev/null 2>&1; then
    return
  fi
  if [ "$(uname -s)" != "Darwin" ] || ! command -v brew >/dev/null 2>&1; then
    printf '%s\n' "k3d is required. Install it from https://k3d.io/ and rerun this command." >&2
    return 1
  fi
  printf '%s\n' "k3d is missing; installing it with Homebrew..."
  brew install k3d
}

container_proxy() {
  printf '%s' "$1" | sed \
    -e 's#127\.0\.0\.1#host.k3d.internal#g' \
    -e 's#localhost#host.k3d.internal#g'
}

create_runtime_secret() {
  secret_file=$(mktemp "${TMPDIR:-/tmp}/agent-platform-ha-secret.XXXXXX")
  trap 'rm -f "$secret_file"' EXIT HUP INT TERM
  chmod 600 "$secret_file"

  mysql_database=$(read_env MYSQL_DATABASE)
  mysql_user=$(read_env MYSQL_USER)
  mysql_password=$(read_env MYSQL_PASSWORD)
  if [ -z "$mysql_database" ] || [ -z "$mysql_user" ] || [ -z "$mysql_password" ]; then
    printf '%s\n' "MYSQL_DATABASE, MYSQL_USER, and MYSQL_PASSWORD must be configured in .env." >&2
    return 1
  fi
  cluster_mysql_dsn="${mysql_user}:${mysql_password}@tcp(host.k3d.internal:3306)/${mysql_database}"
  cluster_mysql_dsn="${cluster_mysql_dsn}?parseTime=true&charset=utf8mb4&loc=UTC"

  {
    printf 'MYSQL_DSN=%s\n' "$cluster_mysql_dsn"
    printf 'LLM_BASE_URL=%s\n' "$LLM_BASE_URL"
    printf 'LLM_API_KEY=%s\n' "$LLM_API_KEY"
    printf 'LLM_MODEL=%s\n' "$LLM_MODEL"
    printf 'HTTP_PROXY=%s\n' "$(container_proxy "$HTTP_PROXY")"
    printf 'HTTPS_PROXY=%s\n' "$(container_proxy "$HTTPS_PROXY")"
    printf 'ALL_PROXY=%s\n' "$(container_proxy "$ALL_PROXY")"
    printf 'NO_PROXY=%s\n' "localhost,127.0.0.1,.svc,.cluster.local,mysql,redis,api,workspace-mcp"
  } > "$secret_file"

  kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
    create secret generic agent-platform-runtime \
    --from-env-file="$secret_file" \
    --dry-run=client \
    --output=yaml | kubectl --context "$HA_KUBE_CONTEXT" apply --filename=-
  rm -f "$secret_file"
  trap - EXIT HUP INT TERM
}

build_images() {
  build_proxy=$(read_env DOCKER_PROXY_URL)
  build_all_proxy=$(read_env DOCKER_ALL_PROXY_URL)
  build_no_proxy=$(read_env DOCKER_NO_PROXY)
  docker build \
    --build-arg "HTTP_PROXY=$build_proxy" \
    --build-arg "HTTPS_PROXY=$build_proxy" \
    --build-arg "ALL_PROXY=$build_all_proxy" \
    --build-arg "NO_PROXY=$build_no_proxy" \
    --tag agent-platform-migrate:ha \
    --file backend/docker/migrate.Dockerfile backend
  docker build \
    --build-arg "HTTP_PROXY=$build_proxy" \
    --build-arg "HTTPS_PROXY=$build_proxy" \
    --build-arg "ALL_PROXY=$build_all_proxy" \
    --build-arg "NO_PROXY=$build_no_proxy" \
    --tag agent-platform-api:ha \
    --file backend/docker/api.Dockerfile backend
  docker build \
    --build-arg "HTTP_PROXY=$build_proxy" \
    --build-arg "HTTPS_PROXY=$build_proxy" \
    --build-arg "ALL_PROXY=$build_all_proxy" \
    --build-arg "NO_PROXY=$build_no_proxy" \
    --tag agent-platform-worker:ha \
    --file backend/docker/worker.Dockerfile backend
  docker build \
    --build-arg "HTTP_PROXY=$build_proxy" \
    --build-arg "HTTPS_PROXY=$build_proxy" \
    --build-arg "ALL_PROXY=$build_all_proxy" \
    --build-arg "NO_PROXY=$build_no_proxy" \
    --tag agent-platform-workspace-mcp:ha \
    --file mcp-server/Dockerfile mcp-server
  docker build \
    --build-arg "HTTP_PROXY=$build_proxy" \
    --build-arg "HTTPS_PROXY=$build_proxy" \
    --build-arg "ALL_PROXY=$build_all_proxy" \
    --build-arg "NO_PROXY=$build_no_proxy" \
    --tag agent-platform-web:ha \
    --file frontend/Dockerfile frontend
}

create_cluster() {
  if k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -qx "$HA_CLUSTER"; then
    return
  fi
  k3d cluster create "$HA_CLUSTER" \
    --servers 1 \
    --agents 2 \
    --api-port "127.0.0.1:$HA_API_PORT" \
    --port "5173:80@loadbalancer" \
    --volume "$REPOS_DIR:/workspace/repos@all" \
    --wait
}

wait_for_kubernetes() {
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    if kubectl --context "$HA_KUBE_CONTEXT" get --raw=/readyz >/dev/null 2>&1; then
      return
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "Kubernetes API did not become ready for context $HA_KUBE_CONTEXT." >&2
  return 1
}

start_dashboard() {
  mkdir -p "$HA_STATE_DIR"
  dashboard_pid_file="$HA_STATE_DIR/dashboard.pid"
  if [ -f "$dashboard_pid_file" ]; then
    dashboard_pid=$(sed -n '1p' "$dashboard_pid_file")
    if [ -n "$dashboard_pid" ] && kill -0 "$dashboard_pid" 2>/dev/null; then
      kill "$dashboard_pid"
    fi
    rm -f "$dashboard_pid_file"
  fi
  run_go_in_module backend build -o "$LOCAL_BIN_DIR/ha-dashboard" ./cmd/ha-dashboard
  nohup env \
    HA_NAMESPACE="$HA_NAMESPACE" \
    HA_KUBE_CONTEXT="$HA_KUBE_CONTEXT" \
    HA_DASHBOARD_ADDR="$HA_DASHBOARD_ADDR" \
    "$LOCAL_BIN_DIR/ha-dashboard" > "$HA_STATE_DIR/dashboard.log" 2>&1 </dev/null &
  dashboard_pid=$!
  printf '%s\n' "$dashboard_pid" > "$dashboard_pid_file"
  dashboard_url="http://$HA_DASHBOARD_ADDR"
  if ! wait_for_url "HA Dashboard" "$dashboard_url/api/pods"; then
    return 1
  fi
}

require_command docker "Install and start Docker Desktop."
require_command kubectl "Install kubectl."
require_command go "Install Go 1.25 or newer."
require_command curl "Install curl."
install_k3d_on_macos
docker compose version >/dev/null
docker info >/dev/null
load_local_configuration

cd "$REPOSITORY_ROOT"
mkdir -p "$HA_STATE_DIR"
for role in web worker api mcp; do
  stop_role "$role"
done
docker compose -f compose.full.yaml stop api worker workspace-mcp web >/dev/null 2>&1 || true
docker_compose up -d --wait mysql redis
build_images
create_cluster
k3d kubeconfig merge "$HA_CLUSTER" --kubeconfig-switch-context
wait_for_kubernetes
k3d image import --cluster "$HA_CLUSTER" \
  agent-platform-migrate:ha \
  agent-platform-api:ha \
  agent-platform-worker:ha \
  agent-platform-workspace-mcp:ha \
  agent-platform-web:ha

kubectl --context "$HA_KUBE_CONTEXT" apply --filename deploy/ha/namespace.yaml
kubectl --context "$HA_KUBE_CONTEXT" apply --filename deploy/ha/config.yaml
create_runtime_secret
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  delete job database-migrate --ignore-not-found
kubectl --context "$HA_KUBE_CONTEXT" apply --filename deploy/ha/migrate.yaml
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" wait \
  --for=condition=complete job/database-migrate \
  --timeout=120s
kubectl --context "$HA_KUBE_CONTEXT" apply --filename deploy/ha/workloads.yaml
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" rollout restart \
  deployment/workspace-mcp deployment/api deployment/worker deployment/web
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  rollout status deployment/workspace-mcp --timeout=120s
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  rollout status deployment/api --timeout=120s
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  rollout status deployment/worker --timeout=120s
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  rollout status deployment/web --timeout=120s
start_dashboard
wait_for_url "Agent Platform" "http://127.0.0.1:5173/"

printf '%s\n' \
  "Agent Platform HA lab is ready:" \
  "  Application:  http://127.0.0.1:5173" \
  "  HA Dashboard: http://$HA_DASHBOARD_ADDR" \
  "  Namespace:    $HA_NAMESPACE" \
  "Run ./scripts/ha/down.sh when the experiment is complete."
