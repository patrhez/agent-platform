#!/usr/bin/env sh

initialize_local_paths() {
  LOCAL_STATE_DIR=${LOCAL_STATE_DIR:-"$REPOSITORY_ROOT/.local"}
  LOCAL_BIN_DIR="$LOCAL_STATE_DIR/bin"
  LOCAL_RUN_DIR="$LOCAL_STATE_DIR/run"
  LOCAL_LOG_DIR="$LOCAL_STATE_DIR/logs"
  mkdir -p "$LOCAL_BIN_DIR" "$LOCAL_RUN_DIR" "$LOCAL_LOG_DIR"
  STARTED_ROLES=""
}

read_env() {
  key=$1
  value=$(printenv "$key" 2>/dev/null || true)
  if [ -n "$value" ]; then
    printf '%s' "$value"
    return
  fi
  if [ -f "${ENV_FILE:-$REPOSITORY_ROOT/.env}" ]; then
    sed -n "s/^${key}=//p" "${ENV_FILE:-$REPOSITORY_ROOT/.env}" | head -n 1
  fi
}

require_command() {
  name=$1
  guidance=$2
  if ! command -v "$name" >/dev/null 2>&1; then
    printf '%s\n' "$name is required. $guidance" >&2
    return 1
  fi
}

select_container_runtime() {
  requested_runtime=${1:-${CONTAINER_RUNTIME:-}}
  runtime_file="$LOCAL_STATE_DIR/container-runtime"
  if [ -z "$requested_runtime" ] && [ -f "$runtime_file" ]; then
    requested_runtime=$(sed -n '1p' "$runtime_file")
  fi
  if [ -z "$requested_runtime" ]; then
    requested_runtime=docker
  fi
  case "$requested_runtime" in
    docker|podman) ;;
    *)
      printf '%s\n' "Unsupported container runtime: $requested_runtime (expected docker or podman)." >&2
      return 2
      ;;
  esac
  CONTAINER_RUNTIME=$requested_runtime
  export CONTAINER_RUNTIME
  if [ "$CONTAINER_RUNTIME" = "podman" ] && [ -z "${PODMAN_COMPOSE_PROVIDER:-}" ]; then
    PODMAN_COMPOSE_PROVIDER=podman-compose
    PODMAN_COMPOSE_WARNING_LOGS=false
    export PODMAN_COMPOSE_PROVIDER PODMAN_COMPOSE_WARNING_LOGS
  fi
  printf '%s\n' "$CONTAINER_RUNTIME" > "$runtime_file"
}

compose_with_runtime() {
  runtime=$1
  shift
  environment_file=${ENV_FILE:-$REPOSITORY_ROOT/.env}
  if [ -f "$environment_file" ]; then
    "$runtime" compose --env-file "$environment_file" "$@"
    return
  fi
  "$runtime" compose "$@"
}

container_compose() {
  compose_with_runtime "$CONTAINER_RUNTIME" "$@"
}

docker_compose() {
  compose_with_runtime docker "$@"
}

wait_for_compose_service() {
  service=$1
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    container_id=$(container_compose ps --quiet "$service" 2>/dev/null || true)
    if [ -n "$container_id" ]; then
      container_status=$(
        "$CONTAINER_RUNTIME" inspect \
          --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
          "$container_id" 2>/dev/null || true
      )
      case "$container_status" in
        healthy|running) return 0 ;;
      esac
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "$service did not become healthy under $CONTAINER_RUNTIME." >&2
  return 1
}

run_go_in_module() {
  module=$1
  shift
  GOMODCACHE="$REPOSITORY_ROOT/$module/.gomodcache" \
    GOCACHE="$REPOSITORY_ROOT/$module/.gocache" \
    go -C "$module" "$@"
}

append_no_proxy() {
  entry=$1
  case ",$NO_PROXY," in
    *",$entry,"*) ;;
    *) NO_PROXY="${NO_PROXY:+$NO_PROXY,}$entry" ;;
  esac
}

load_local_configuration() {
  REPOS_DIR=$(read_env REPOS_DIR)
  MYSQL_DSN=$(read_env MYSQL_DSN)
  REDIS_URL=$(read_env REDIS_URL)
  LLM_BASE_URL=$(read_env LLM_BASE_URL)
  LLM_API_KEY=$(read_env LLM_API_KEY)
  LLM_MODEL=$(read_env LLM_MODEL)
  WORKSPACE_MCP_URL=$(read_env WORKSPACE_MCP_URL)
  WORKER_ID=$(read_env WORKER_ID)
  HTTPS_PROXY=$(read_env HTTPS_PROXY)
  HTTP_PROXY=$(read_env HTTP_PROXY)
  ALL_PROXY=$(read_env ALL_PROXY)
  NO_PROXY=$(read_env NO_PROXY)
  if [ -z "$NO_PROXY" ]; then NO_PROXY=$(read_env no_proxy); fi
  append_no_proxy 127.0.0.1
  append_no_proxy localhost
  no_proxy=$NO_PROXY
  export REPOS_DIR MYSQL_DSN REDIS_URL LLM_BASE_URL LLM_API_KEY LLM_MODEL WORKSPACE_MCP_URL WORKER_ID
  export HTTPS_PROXY HTTP_PROXY ALL_PROXY NO_PROXY no_proxy
  for key in REPOS_DIR MYSQL_DSN REDIS_URL LLM_BASE_URL LLM_API_KEY LLM_MODEL WORKSPACE_MCP_URL WORKER_ID; do
    value=$(printenv "$key")
    if [ -z "$value" ]; then
      printf '%s\n' "$key must be configured in .env or the process environment." >&2
      return 1
    fi
  done
  if [ ! -d "$REPOS_DIR" ]; then
    printf '%s\n' "REPOS_DIR is not a directory: $REPOS_DIR" >&2
    return 1
  fi
}

pid_is_live() {
  role=$1
  pid_file="$LOCAL_RUN_DIR/$role.pid"
  [ -f "$pid_file" ] || return 1
  pid=$(sed -n '1p' "$pid_file")
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

start_role() {
  role=$1
  shift
  if pid_is_live "$role"; then
    printf '%s\n' "$role is already running with PID $(sed -n '1p' "$LOCAL_RUN_DIR/$role.pid")."
    return 0
  fi
  rm -f "$LOCAL_RUN_DIR/$role.pid"
  if [ -n "${LOCAL_COMMAND_LOG:-}" ]; then
    printf 'start %s' "$role" >> "$LOCAL_COMMAND_LOG"
    for argument in "$@"; do printf ' %s' "$argument" >> "$LOCAL_COMMAND_LOG"; done
    printf '\n' >> "$LOCAL_COMMAND_LOG"
    printf '%s\n' "$$" > "$LOCAL_RUN_DIR/$role.pid"
  else
    nohup "$@" >> "$LOCAL_LOG_DIR/$role.log" 2>&1 </dev/null &
    pid=$!
    printf '%s\n' "$pid" > "$LOCAL_RUN_DIR/$role.pid"
    sleep 1
    if ! kill -0 "$pid" 2>/dev/null; then
      printf '%s\n' "$role failed to start; see $LOCAL_LOG_DIR/$role.log" >&2
      return 1
    fi
  fi
  STARTED_ROLES="$role $STARTED_ROLES"
}

stop_role() {
  role=$1
  if [ -n "${LOCAL_COMMAND_LOG:-}" ]; then
    printf '%s\n' "stop $role" >> "$LOCAL_COMMAND_LOG"
    rm -f "$LOCAL_RUN_DIR/$role.pid"
    return 0
  fi
  pid_file="$LOCAL_RUN_DIR/$role.pid"
  [ -f "$pid_file" ] || return 0
  pid=$(sed -n '1p' "$pid_file")
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    attempts=0
    while kill -0 "$pid" 2>/dev/null && [ "$attempts" -lt 20 ]; do
      sleep 0.25
      attempts=$((attempts + 1))
    done
  fi
  rm -f "$pid_file"
}

supervise_roles() {
  if [ -n "${LOCAL_COMMAND_LOG:-}" ]; then
    printf 'supervise' >> "$LOCAL_COMMAND_LOG"
    for role in "$@"; do printf ' %s' "$role" >> "$LOCAL_COMMAND_LOG"; done
    printf '\n' >> "$LOCAL_COMMAND_LOG"
    return 0
  fi
  while :; do
    for role in "$@"; do
      if ! pid_is_live "$role"; then
        printf '%s\n' "$role stopped unexpectedly; see $LOCAL_LOG_DIR/$role.log" >&2
        return 1
      fi
    done
    sleep 1
  done
}

wait_for_url() {
  name=$1
  url=$2
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    if curl --fail --silent --show-error --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "$name did not become healthy at $url" >&2
  return 1
}
