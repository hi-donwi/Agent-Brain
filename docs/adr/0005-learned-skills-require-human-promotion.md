# ADR-0005: Learned skills are untrusted candidates until a human promotes them

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

The skill forge (M7) turns repeated, successful work recorded in agent traces into new
skills, then tests and refines them. Automating this is the point, but a skill is an
instruction the agent will follow later, with whatever tools it has.

Traces contain text the agent read: files, retrieved documents, tool output, web content
fetched through MCP tools. Any of it can carry prompt injection. A forge that writes
skills from traces and activates them on its own is a path for untrusted text to become a
standing instruction. It can also learn a wrong procedure from a run that only looked
successful, or copy a secret from a trace into a skill.

## Decision

The forge only ever produces **candidates**. A candidate lives in a separate directory,
is never visible to the skill router, and becomes a skill only through an explicit human
action (`lokal skills promote <candidate>`), which shows the full skill, its provenance,
and its evaluation report first.

Before a candidate can be promoted it must:

1. Record provenance: the trace IDs it was derived from, and whether any of them included
   content from outside the user's own files (flagged prominently).
2. Pass structural validation (ADR-0004) and a secret and personal-data scan.
3. Have an evaluation on fixtures that include a direct request, a paraphrase, a nearby
   task that must not trigger it, and a failure or adversarial case, run with and without
   the skill on the same fixtures. Unrun cases count as failures, never as passes.

Promoted skills are versioned by content hash; `lokal skills rollback` restores the
previous version. Hardening rounds that change the fixtures invalidate earlier results
instead of being compared against them.

## Consequences

### Positive
- No path from fetched content to an active instruction without a human reading it.
- Every promoted skill comes with evidence that it helps and does not misfire.
- Promotion produces a folder ready to propose upstream to Agent-Skills.

### Negative
- The forge is not fully autonomous; skills accumulate as candidates until reviewed.
- Evaluation costs local compute time per candidate (bounded by a round limit).

### Neutral
- A later ADR may allow automatic promotion for narrowly defined, low-risk classes once
  there is data on candidate quality. This ADR does not.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Auto-activate skills that pass evals | Evals written by the same model that wrote the skill do not catch injected intent |
| Learn only from user-written notes, not traces | Loses the main signal: what actually worked |
| Store learned procedures as memory, not skills | Unreviewable, unversioned, and not portable to other agents |

## Follow-up

- [ ] M5: traces record content origin per message so provenance is possible
- [ ] M7: candidate store, eval harness, promote and rollback commands
