#!/usr/bin/env sh

set -eu

HA_NAMESPACE=${HA_NAMESPACE:-agent-platform-ha}
HA_KUBE_CONTEXT=${HA_KUBE_CONTEXT:-k3d-agent-platform-ha}
kubectl --context "$HA_KUBE_CONTEXT" --namespace "$HA_NAMESPACE" get pods \
  --selector 'app.kubernetes.io/component in (api,worker,mcp,web)' \
  --output wide
