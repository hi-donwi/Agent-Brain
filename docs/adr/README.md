# Architecture decision records

Decisions that are expensive to reverse. An ADR is never edited after it is accepted
except to mark it superseded; a changed decision gets a new ADR that names the old one.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-single-static-binary.md) | lokal is one static Go binary with CGO disabled | Accepted |
| [0002](0002-orchestrate-existing-inference-backends.md) | Orchestrate existing inference backends; never run models in-process | Accepted |
| [0003](0003-sqlite-hybrid-retrieval-store.md) | One pure-Go SQLite store with FTS5 and in-process vector search | Accepted |
| [0004](0004-agent-skills-format-and-progressive-disclosure.md) | Skills use the Agent Skills SKILL.md format, loaded by progressive disclosure | Accepted |
| [0005](0005-learned-skills-require-human-promotion.md) | Learned skills are untrusted candidates until a human promotes them | Accepted |
| [0006](0006-local-only-by-default.md) | Local only by default: loopback, per-process token, no egress, no telemetry | Accepted |

New ADRs use the next number and the same sections: Context, Decision, Consequences
(positive, negative, neutral), Alternatives considered, Follow-up.
