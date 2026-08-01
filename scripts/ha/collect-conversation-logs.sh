#!/usr/bin/env sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}

conversation_id=""
since="24h"
from_time=""
to_time=""
output_path=""
include_previous=1

usage() {
  printf '%s\n' \
    "Usage: ./scripts/ha/collect-conversation-logs.sh CONVERSATION_ID [options]" \
    "" \
    "Collect api + worker pod logs for a conversation, expand related run_id/trace_id," \
    "and write a greppable file under tmp/." \
    "" \
    "Options:" \
    "  --since DURATION     Relative window for kubectl (default: 24h). Ignored if --from is set." \
    "  --from RFC3339       Absolute start time (kubectl --since-time)." \
    "  --to RFC3339         Absolute end time (client-side filter; kubectl has no --until)." \
    "  -o, --output PATH    Output file (default: tmp/conversation-<id>.log)." \
    "  --no-previous        Skip terminated previous containers." \
    "  -h, --help           Show this help." \
    "" \
    "Env:" \
    "  HA_NAMESPACE         Kubernetes namespace (default: agent-platform-ha)" \
    "  HA_KUBE_CONTEXT      kubectl context (default: k3d-agent-platform-ha)"
}

die() {
  printf '%s\n' "$*" >&2
  exit 2
}

require_command() {
  name=$1
  guidance=$2
  if ! command -v "$name" >/dev/null 2>&1; then
    die "$name is required. $guidance"
  fi
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -h|--help)
        usage
        exit 0
        ;;
      --since)
        [ "$#" -ge 2 ] || die "--since requires a value"
        since=$2
        shift 2
        ;;
      --from)
        [ "$#" -ge 2 ] || die "--from requires a value"
        from_time=$2
        shift 2
        ;;
      --to)
        [ "$#" -ge 2 ] || die "--to requires a value"
        to_time=$2
        shift 2
        ;;
      -o|--output)
        [ "$#" -ge 2 ] || die "$1 requires a value"
        output_path=$2
        shift 2
        ;;
      --no-previous)
        include_previous=0
        shift
        ;;
      --)
        shift
        break
        ;;
      -*)
        die "Unknown option: $1"
        ;;
      *)
        if [ -n "$conversation_id" ]; then
          die "Unexpected argument: $1"
        fi
        conversation_id=$1
        shift
        ;;
    esac
  done
  if [ "$#" -gt 0 ]; then
    die "Unexpected argument: $1"
  fi
  if [ -z "$conversation_id" ]; then
    usage >&2
    die "CONVERSATION_ID is required."
  fi
}

list_pods() {
  kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" get pods \
    --selector 'app.kubernetes.io/component in (api,worker)' \
    --field-selector=status.phase=Running \
    --output=jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.labels.app\.kubernetes\.io/component}{"\n"}{end}'
}

prefix_and_filter_lines() {
  pod_name=$1
  role=$2
  # Keep stdin free for the kubectl pipe (no heredoc).
  TO_TIME=$to_time POD_NAME=$pod_name ROLE=$role python3 -c '
import os
import re
import sys
from datetime import datetime, timezone

FRAC_RE = re.compile(r"(\.\d+)")

def parse_ts(value):
    value = value.strip()
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    # kubectl timestamps can include 9-digit nanos; fromisoformat accepts <=6.
    match = FRAC_RE.search(value)
    if match is not None:
        frac = match.group(1)
        if len(frac) > 7:
            value = value[: match.start()] + frac[:7] + value[match.end() :]
    parsed = datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=timezone.utc)
    return parsed

cutoff = None
raw_cutoff = os.environ.get("TO_TIME", "").strip()
if raw_cutoff:
    cutoff = parse_ts(raw_cutoff)

pod = os.environ["POD_NAME"]
role = os.environ["ROLE"]
for line in sys.stdin:
    line = line.rstrip("\n")
    if not line:
        continue
    if cutoff is not None:
        parts = line.split(" ", 1)
        if len(parts) != 2:
            continue
        try:
            ts = parse_ts(parts[0])
        except ValueError:
            continue
        if ts > cutoff:
            continue
    sys.stdout.write(f"pod={pod} role={role} {line}\n")
'
}

fetch_pod_logs() {
  pod_name=$1
  role=$2
  raw_file=$3

  set -- kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
    logs "$pod_name" --timestamps=true
  if [ -n "$from_time" ]; then
    set -- "$@" --since-time="$from_time"
  else
    set -- "$@" --since="$since"
  fi

  "$@" 2>/dev/null | prefix_and_filter_lines "$pod_name" "$role" >> "$raw_file" || true

  if [ "$include_previous" -eq 1 ]; then
    "$@" --previous 2>/dev/null | prefix_and_filter_lines "$pod_name" "$role" >> "$raw_file" || true
  fi
}

extract_related_ids() {
  seed_file=$1
  ids_file=$2
  CONVERSATION_ID=$conversation_id python3 - "$seed_file" "$ids_file" <<'PY'
import json
import os
import re
import sys

conversation_id = os.environ["CONVERSATION_ID"]
seed_path, ids_path = sys.argv[1], sys.argv[2]
run_ids = set()
trace_ids = set()
field_re = re.compile(r'"(run_id|trace_id)"\s*:\s*"([^"]+)"')

with open(seed_path, encoding="utf-8") as handle:
    for line in handle:
        for key, value in field_re.findall(line):
            if key == "run_id":
                run_ids.add(value)
            else:
                trace_ids.add(value)
        start = line.find("{")
        if start < 0:
            continue
        try:
            payload = json.loads(line[start:])
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        for key, bucket in (("run_id", run_ids), ("trace_id", trace_ids)):
            value = payload.get(key)
            if isinstance(value, str) and value:
                bucket.add(value)

with open(ids_path, "w", encoding="utf-8") as handle:
    handle.write(f"conversation_id={conversation_id}\n")
    for value in sorted(run_ids):
        handle.write(f"run_id={value}\n")
    for value in sorted(trace_ids):
        handle.write(f"trace_id={value}\n")
PY
}

filter_expanded() {
  raw_file=$1
  ids_file=$2
  matched_file=$3
  CONVERSATION_ID=$conversation_id python3 - "$ids_file" "$raw_file" "$matched_file" <<'PY'
import os
import re
import sys

conversation_id = os.environ["CONVERSATION_ID"]
ids_path, raw_path, matched_path = sys.argv[1], sys.argv[2], sys.argv[3]
needles = {conversation_id}
with open(ids_path, encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line or "=" not in line:
            continue
        _, value = line.split("=", 1)
        if value:
            needles.add(value)

ordered = sorted(needles, key=len, reverse=True)
pattern = re.compile("|".join(re.escape(item) for item in ordered))
seen = set()
with open(raw_path, encoding="utf-8") as source, open(matched_path, "w", encoding="utf-8") as sink:
    for line in source:
        if not pattern.search(line):
            continue
        if line in seen:
            continue
        seen.add(line)
        sink.write(line)
PY
}

sort_by_timestamp() {
  input_file=$1
  output_file=$2
  python3 - "$input_file" "$output_file" <<'PY'
import sys

input_path, output_path = sys.argv[1], sys.argv[2]

def sort_key(line: str) -> str:
    parts = line.split(" ", 3)
    if len(parts) >= 3:
        return parts[2]
    return line

with open(input_path, encoding="utf-8") as handle:
    lines = handle.readlines()
lines.sort(key=sort_key)
with open(output_path, "w", encoding="utf-8") as handle:
    handle.writelines(lines)
PY
}

write_header() {
  output_file=$1
  ids_file=$2
  {
    printf '# conversation log collection\n'
    printf '# conversation_id=%s\n' "$conversation_id"
    if [ -n "$from_time" ]; then
      printf '# from=%s\n' "$from_time"
    else
      printf '# since=%s\n' "$since"
    fi
    if [ -n "$to_time" ]; then
      printf '# to=%s\n' "$to_time"
    fi
    printf '# context=%s namespace=%s\n' "$HA_KUBE_CONTEXT" "$HA_NAMESPACE"
    printf '# related ids:\n'
    sed 's/^/#   /' "$ids_file"
    printf '#\n'
  } > "$output_file"
}

main() {
  parse_args "$@"
  require_command kubectl "Install kubectl and ensure the HA lab is up."
  require_command python3 "Install Python 3."

  if [ -z "$output_path" ]; then
    output_path="$REPOSITORY_ROOT/tmp/conversation-${conversation_id}.log"
  fi
  case "$output_path" in
    /*) ;;
    *) output_path="$REPOSITORY_ROOT/$output_path" ;;
  esac
  mkdir -p "$(dirname -- "$output_path")"

  work_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-platform-conv-logs.XXXXXX")
  trap 'rm -rf "$work_dir"' EXIT
  raw_file="$work_dir/raw.log"
  seed_file="$work_dir/seed.log"
  ids_file="$work_dir/ids.txt"
  matched_file="$work_dir/matched.log"
  sorted_file="$work_dir/sorted.log"
  : > "$raw_file"

  pod_count=0
  while IFS="$(printf '\t')" read -r pod_name role; do
    [ -n "$pod_name" ] || continue
    case "$role" in
      api|worker) ;;
      *) continue ;;
    esac
    printf 'Collecting %s (%s)...\n' "$pod_name" "$role" >&2
    fetch_pod_logs "$pod_name" "$role" "$raw_file"
    pod_count=$((pod_count + 1))
  done <<EOF
$(list_pods)
EOF

  if [ "$pod_count" -eq 0 ]; then
    die "No running api/worker pods found in $HA_NAMESPACE (context $HA_KUBE_CONTEXT)."
  fi

  grep -F -- "$conversation_id" "$raw_file" > "$seed_file" || true
  extract_related_ids "$seed_file" "$ids_file"
  filter_expanded "$raw_file" "$ids_file" "$matched_file"
  sort_by_timestamp "$matched_file" "$sorted_file"
  write_header "$output_path" "$ids_file"
  cat "$sorted_file" >> "$output_path"

  line_count=$(wc -l < "$sorted_file" | tr -d ' ')
  related_count=$(grep -c . "$ids_file" || true)
  printf 'Wrote %s lines (%s related ids) to %s\n' "$line_count" "$related_count" "$output_path"
}

main "$@"
