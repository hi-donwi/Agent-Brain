# Architecture decision records

Decisions that are expensive to reverse. The decision in an accepted ADR is never
edited; a changed decision gets a new ADR that names the old one, and the old one is
marked superseded. Editorial fixes that do not change the decision, such as milestone
numbers after the roadmap is renumbered, are allowed.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-single-static-binary.md) | lokal is one static Go binary with CGO disabled | Accepted |
| [0002](0002-orchestrate-existing-inference-backends.md) | Orchestrate existing inference backends; never run models in-process | Accepted |
| [0003](0003-sqlite-hybrid-retrieval-store.md) | One pure-Go SQLite store with FTS5 and in-process vector search | Accepted |
| [0004](0004-agent-skills-format-and-progressive-disclosure.md) | Skills use the Agent Skills SKILL.md format, loaded by progressive disclosure | Accepted |
| [0005](0005-learned-skills-require-human-promotion.md) | Learned skills are untrusted candidates until a human promotes them | Accepted |
| [0006](0006-local-only-by-default.md) | Local only by default: loopback, per-process token, no egress, no telemetry | Accepted |
| [0007](0007-agent-runtime-coding-and-multi-agent-scope.md) | lokal includes an agent runtime, a coding agent, and multi-agent orchestration; UIs stay outside | Superseded by 0008 |
| [0008](0008-local-ai-platform-agent-hosts-own-the-loop.md) | lokal is the local AI platform; agent hosts own the agent loop | Accepted |
| [0009](0009-gateway-architecture-and-loopback-security.md) | Gateway architecture, loopback security, role routing, and context guard | Accepted |
| [0010](0010-remote-access-and-gateway-as-backend.md) | Remote access, SSH tunnels, WireGuard overlays, and Gateway-as-Backend | Accepted |
| [0013](0013-platform-owns-agent-policy-contracts-host-executes.md) | The platform owns agent policy contracts (instructions, permissions, sub-agents); the host executes | Accepted |
| [0014](0014-profile-scoped-inference.md) | Inference credentials are scoped to content-addressed routing profile revisions and protocol sets | Accepted |
| [0015](0015-brain-management-api.md) | Separate Brain management credentials and typed provider/account/profile APIs | Accepted |
| [0016](0016-narrow-brain-origin-management-console.md) | Brain-origin operator UI is a narrow exception to no general Brain web UI | Accepted |
| [0017](0017-typed-local-backend-and-model-management.md) | Catalog-bounded local backend/model operations with confirmation and safety gates | Accepted |
| [0018](0018-configured-root-rag-management.md) | RAG management uses CLI-registered canonical roots and local embeddings only | Accepted |
| [0019](0019-user-skills-and-mcp-management-inventory.md) | Unscoped management lists Brain-user skills and static read-only MCP metadata only | Accepted |
| [0020](0020-safe-trace-inventory-and-local-smoke-evaluation.md) | Trace summaries are pseudonymous; evaluation is a fixed local smoke suite | Accepted |
| [0021](0021-unscoped-forge-candidate-management.md) | Forge promotion in Settings is gated by local human review and safety evidence | Accepted |
| [0022](0022-redacted-service-status-and-cli-owned-lifecycle.md) | Service status is redacted; OS install/uninstall remains CLI-owned | Accepted |
| [0023](0023-single-file-user-skill-import.md) | Unscoped Settings may install one validated Brain-user SKILL.md only | Accepted |

New ADRs use the next number and the same sections: Context, Decision, Consequences
(positive, negative, neutral), Alternatives considered, Follow-up.
