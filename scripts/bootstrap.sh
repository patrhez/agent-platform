#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$REPOSITORY_ROOT/scripts/lib/local.sh"
initialize_local_paths

usage() {
  printf '%s\n' \
    "Usage: ./scripts/bootstrap.sh [--runtime docker|podman] [--env-file PATH]" \
    "Checks macOS dependencies, starts the selected runtime, and launches Agent Platform."
}

requested_runtime=docker
while [ "$#" -gt 0 ]; do
  case "$1" in
    --runtime)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      requested_runtime=$2
      shift 2
      ;;
    --runtime=*) requested_runtime=${1#*=}; shift ;;
    --env-file)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      ENV_FILE=$2
      export ENV_FILE
      shift 2
      ;;
    --env-file=*) ENV_FILE=${1#*=}; export ENV_FILE; shift ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
select_container_runtime "$requested_runtime"

ensure_environment_file() {
  environment_file=${ENV_FILE:-$REPOSITORY_ROOT/.env}
  if [ -f "$environment_file" ]; then
    return
  fi
  if [ -n "${ENV_FILE:-}" ]; then
    printf '%s\n' "Environment file does not exist: $environment_file" >&2
    return 1
  fi
  cp "$REPOSITORY_ROOT/.env.example" "$environment_file"
  printf '%s\n' \
    "Created $environment_file." \
    "Set REPOS_DIR, LLM_BASE_URL, LLM_API_KEY, and LLM_MODEL, then rerun this command." >&2
  return 1
}

activate_homebrew() {
  if command -v brew >/dev/null 2>&1; then
    return
  fi
  if [ -x /opt/homebrew/bin/brew ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
    return
  fi
  if [ -x /usr/local/bin/brew ]; then
    eval "$(/usr/local/bin/brew shellenv)"
  fi
}

ensure_homebrew() {
  activate_homebrew
  if command -v brew >/dev/null 2>&1; then
    return
  fi
  printf '%s\n' "Homebrew is missing; installing it non-interactively..."
  NONINTERACTIVE=1 /bin/bash -c \
    "$(curl --fail --silent --show-error --location https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  activate_homebrew
  require_command brew "Homebrew installation did not add brew to PATH. Open a new terminal and rerun."
}

install_formula_for_command() {
  command_name=$1
  formula=$2
  if command -v "$command_name" >/dev/null 2>&1; then
    return
  fi
  ensure_homebrew
  printf '%s\n' "Installing $formula with Homebrew..."
  brew install "$formula"
  require_command "$command_name" "Homebrew installed $formula, but $command_name is not in PATH."
}

version_at_least() {
  actual_version=$1
  minimum_version=$2
  awk -v actual="$actual_version" -v minimum="$minimum_version" 'BEGIN {
    split(actual, a, "."); split(minimum, m, ".")
    for (i = 1; i <= 3; i++) {
      av = a[i] + 0; mv = m[i] + 0
      if (av > mv) exit 0
      if (av < mv) exit 1
    }
    exit 0
  }'
}

ensure_go() {
  install_formula_for_command go go
  go_version=$(go version | sed -n 's/.*go\([0-9][0-9.]*\).*/\1/p')
  if ! version_at_least "$go_version" 1.25.0; then
    printf '%s\n' "Go 1.25.0 or newer is required; found ${go_version:-unknown}." >&2
    return 1
  fi
}

ensure_node() {
  install_formula_for_command node node
  install_formula_for_command npm node
  node_version=$(node -p 'process.versions.node')
  if ! version_at_least "$node_version" 20.19.0; then
    printf '%s\n' "Node.js 20.19.0 or newer is required; found ${node_version:-unknown}." >&2
    return 1
  fi
}

wait_for_runtime() {
  attempts=0
  while [ "$attempts" -lt 120 ]; do
    if "$CONTAINER_RUNTIME" info >/dev/null 2>&1; then
      return
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "$CONTAINER_RUNTIME did not become ready within 120 seconds." >&2
  return 1
}

start_docker() {
  if docker info >/dev/null 2>&1; then
    return
  fi
  printf '%s\n' "Starting Docker Desktop..."
  open -a Docker
  wait_for_runtime
}

start_podman() {
  if ! podman info >/dev/null 2>&1; then
    if ! podman machine inspect >/dev/null 2>&1; then
      podman machine init --cpus 4 --memory 6144 --disk-size 60
    fi
    podman machine start >/dev/null 2>&1 || true
    wait_for_runtime
  fi
  install_formula_for_command podman-compose podman-compose
  PODMAN_COMPOSE_PROVIDER=podman-compose
  PODMAN_COMPOSE_WARNING_LOGS=false
  export PODMAN_COMPOSE_PROVIDER PODMAN_COMPOSE_WARNING_LOGS
  podman compose version >/dev/null
}

ensure_container_runtime() {
  case "$CONTAINER_RUNTIME" in
    docker)
      if ! command -v docker >/dev/null 2>&1; then
        ensure_homebrew
        printf '%s\n' "Installing Docker Desktop with Homebrew..."
        brew install --cask docker
      fi
      require_command open "macOS open command is required to launch Docker Desktop."
      start_docker
      docker compose version >/dev/null
      ;;
    podman)
      install_formula_for_command podman podman
      start_podman
      ;;
  esac
}

if [ "$(uname -s)" != "Darwin" ]; then
  printf '%s\n' "Automatic dependency installation currently supports macOS only." >&2
  exit 1
fi

ensure_environment_file
load_local_configuration
require_command curl "curl is included with macOS and is required for health checks."
ensure_go
ensure_node
ensure_container_runtime

printf '%s\n' "Dependencies are ready; starting Agent Platform with $CONTAINER_RUNTIME..."
exec "$REPOSITORY_ROOT/scripts/up.sh" --runtime "$CONTAINER_RUNTIME"
