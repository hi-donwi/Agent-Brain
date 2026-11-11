# ADR-0021: Unscoped Forge candidate management

- **Date:** 2026-11-11
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0005 and ADR-0016 (candidate inspection/promotion from an unscoped console)

## Context

Forge candidates are drafted from traces, and traces may contain project/client prompts, tool
results, secrets, and prompt injection. Agent-Control Settings is not bound to a project and must
not expose candidate names/content/provenance session IDs from another project. ADR-0005 requires
explicit human review, source provenance, structural validation, secret/personal-data scan, and
evaluation before a candidate becomes an active skill.

## Decision

The unscoped Brain console may list opaque Brain-keyed candidate IDs and gate/status metadata, run
the deterministic local fixture evaluator, and discard a candidate after exact confirmation. It
does not return candidate names, descriptions, bodies, trace IDs, filesystem paths, or trace
content. Source-session counts and candidate IDs are pseudonymized with a key derived from the
Brain gateway master.

Review of the full candidate body/provenance is local CLI-only (`brain forge candidates` followed
by `brain skills review-candidate <name>`). The review command prints the candidate, provenance,
and evaluation state, requires explicit `owned-only`/`external-included` classification, and
records the reviewed content hash. The console may promote only a fresh-evaluated, structurally
valid, secret-clean, explicitly reviewed candidate marked `owned-only`; promotion is exact-ID
confirmed and writes only into Brain's user-skill home. Candidates marked external remain
CLI-only. No API runs `forge mine`, imports traces, invokes a shell, or passes raw trace data into
the agent host.

## Consequences

### Positive

- Agent-Control can manage readiness/promotion without receiving trace content or candidate
  instructions in its backend.
- Promotion is bound to the exact reviewed candidate content hash and current evaluation report.
- External-content candidates cannot be promoted through unscoped Settings.

### Negative

- A human must inspect candidate content/provenance locally before the UI enables promotion.
- Candidate source names and raw provenance remain CLI-only; aliases are opaque and stable only
  while the Brain gateway master remains unchanged.
- Forge mining/drafting/hardening from real trace data remains CLI-owned.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Render every candidate in Agent-Control Settings | The page has no project scope and could expose another project's trace-derived instructions. |
| Auto-review/promote after evaluation | Violates ADR-0005; evaluations can miss injected intent and provenance is not automatic. |
| Accept a candidate path/name from HTTP | Creates path traversal and naming-based information disclosure. |
| Expose `forge mine` over HTTP | Reads all trace content without project scope. |

## Follow-up

- [ ] Add project-scoped trace provenance before considering candidate detail in any UI.
- [ ] Keep candidate evaluation deterministic and local; any future model-based evaluator needs its own explicit egress/data contract.
