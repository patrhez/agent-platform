# HA Lab Scripts Handbook

Scripts under `scripts/ha/` operate the local high-availability lab (k3d + kubectl)
and help debug Runs across replicated API and Worker pods.

Defaults:

| Env | Default |
|---|---|
| `HA_CLUSTER` | `agent-platform-ha` |
| `HA_NAMESPACE` | `agent-platform-ha` |
| `HA_KUBE_CONTEXT` | `k3d-agent-platform-ha` |

Most commands assume `./scripts/ha/up.sh` has already created the cluster.

## Lifecycle

### `./scripts/ha/up.sh`

Builds images, creates/starts the k3d cluster, applies manifests, and starts the HA
dashboard. Overlays `LOG_LEVEL` / `LOG_MAX_AGE_DAYS` from `.env` onto
`deploy/ha/config.yaml` before apply.

```sh
./scripts/ha/up.sh
```

### `./scripts/ha/down.sh`

Deletes the k3d cluster and HA dashboard. Pass `--dependencies` to also stop the
Compose MySQL/Redis stack used by the lab.

```sh
./scripts/ha/down.sh
./scripts/ha/down.sh --dependencies
```

## Inspection

### `./scripts/ha/pods.sh`

Lists api / worker / mcp / web pods (`kubectl get pods -o wide`).

```sh
./scripts/ha/pods.sh
```

### `./scripts/ha/logs.sh POD_NAME`

Follows timestamps logs for one pod.

```sh
./scripts/ha/logs.sh api-68698dcf69-4sqtp
```

### `./scripts/ha/shell.sh POD_NAME`

Opens an interactive shell in a pod.

```sh
./scripts/ha/shell.sh worker-67898d647b-hzckz
```

## Conversation log collection

### `./scripts/ha/collect-conversation-logs.sh`

Pulls logs from **all running api + worker pods**, keeps lines for a conversation,
then expands related `run_id` / `trace_id` values discovered in those lines. Writes a
single greppable file (gitignored under `tmp/`).

```sh
# Last 24h (default), write tmp/conversation-<id>.log
./scripts/ha/collect-conversation-logs.sh 01KYYW2GAZ0DZDSX7VPAPFFKDF

# Relative window
./scripts/ha/collect-conversation-logs.sh 01KYYW2GAZ0DZDSX7VPAPFFKDF --since 6h

# Absolute window (--to is client-side; kubectl has no --until)
./scripts/ha/collect-conversation-logs.sh 01KYYW2GAZ0DZDSX7VPAPFFKDF \
  --from 2026-08-01T14:30:00Z \
  --to 2026-08-01T15:00:00Z

# Custom output path
./scripts/ha/collect-conversation-logs.sh 01KYYW2GAZ0DZDSX7VPAPFFKDF -o /tmp/conv.log
```

Output line shape:

```text
pod=<pod> role=api|worker <kubectl-rfc3339> <json-log>
```

Header comments at the top list the time window and every related id used for
expansion. Typical follow-ups:

```sh
rg 'run finished|run failed|http_request' tmp/conversation-01KYYW2GAZ0DZDSX7VPAPFFKDF.log
rg '"msg":"tool' tmp/conversation-01KYYW2GAZ0DZDSX7VPAPFFKDF.log
rg 'trace_id.:.01KYY' tmp/conversation-01KYYW2GAZ0DZDSX7VPAPFFKDF.log
```

Notes:

- Seed match is a substring on the conversation id (JSON field or URL path).
- Expansion pulls additional lines that mention discovered `run_id` / `trace_id`
  values even when those lines omit `conversation_id`.
- Previous containers are included by default (`--no-previous` to skip).
- API HTTP traces and worker execution traces differ across the async claim
  boundary; joining still works via `conversation_id` / `run_id`.

## Failover checks

### `./scripts/ha/test-api-failover.sh`

Deletes an API pod and checks the service remains reachable.

### `./scripts/ha/test-worker-failover.sh`

Exercises worker failover while a Run is in flight.

## Related docs

- Logging / trace id model: [`docs/tech/logging.md`](../../docs/tech/logging.md)
- Runtime streaming FAQ: [`docs/tech/execution-and-streaming-faq.md`](../../docs/tech/execution-and-streaming-faq.md)
