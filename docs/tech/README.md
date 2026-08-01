# Tech Notes

Internal engineering notes for the Agent Platform MVP.

| Document | Topic |
|---|---|
| [execution-and-streaming-faq.md](./execution-and-streaming-faq.md) | SSE / Redis / RunEvent streaming, follow-up Runs, ULID, clientMessageId, Workspace MCP |
| [logging.md](./logging.md) | Zap Logger DI, daily file rotation, trace id middleware, async panic recovery, MustJSON |
| [architecture-quiz.md](./architecture-quiz.md) | Self-test questions on architecture, fields, execution, streaming, and API design |
| [runtime-data-and-execution-flow.md](./runtime-data-and-execution-flow.md) | One real Run through HTTP, MySQL rows, Worker, Checkpoint, and SSE |
| [HA scripts handbook](../../scripts/ha/README.md) | k3d HA lab lifecycle, pod inspection, conversation log collection (`scripts/ha/`) |

Related design baselines live under `docs/superpowers/`.
