#!/usr/bin/env sh

set -eu

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}
pod_name=${1:-}

if [ -z "$pod_name" ] || [ "$#" -ne 1 ]; then
  printf '%s\n' "Usage: ./scripts/ha/shell.sh POD_NAME" >&2
  exit 2
fi

kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" \
  exec --stdin --tty "$pod_name" -- /bin/sh
