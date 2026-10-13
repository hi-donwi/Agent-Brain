# ADR-0007: lokal includes an agent runtime, a coding agent, and multi-agent orchestration; UIs stay outside

- **Date:** 2026-10-13
- **Status:** Proposed
- **Deciders:** Doni Wicaksono

## Context

The first roadmap planned a single bounded agent loop (`lokal run`) as a source of traces
for the skill forge. Users of local AI want more than that from the same machine: a code
assistant that edits a repository safely, a "describe it and build it" flow (vibe coding),
several models working together, several agents dividing a task, and context that
survives long sessions. All of these need the same foundations lokal already plans:
model access (ADR-0002), retrieval (ADR-0003), skills (ADR-0004), review gates
(ADR-0005), and local-only defaults (ADR-0006).

Three constraints shape how far lokal should go:

- **Small local models.** Context windows are short and tool use is unreliable compared
  with frontier models. Features must narrow the problem (fewer tools, explicit budgets,
  structured edits) rather than assume a strong model.
- **Headless hosts.** lokal often runs on a dedicated VM reached over SSH, so the agent
  must be usable from a terminal and drivable by other programs, with no GUI.
- **Existing tools.** Good coding front ends already exist and speak the OpenAI API or MCP
  (opencode, Continue, Aider, Cline, and others), and Agent-Control is the web control
  panel in the same family. Rebuilding their interfaces adds nothing.

## Decision

lokal provides, as one runtime shared by every feature:

1. **Agent runtime**: a bounded tool loop with approvals, a context engine (budgeted
   assembly from ranked sources, compaction, project instruction files, reviewable
   memory), persistent and resumable sessions, and traces.
2. **Coding agent** (`lokal code`) on top of the runtime: repository map, edit formats
   chosen per model, git checkpoints with undo on a branch or worktree, a build/lint/test
   verify loop, and a scaffold-and-iterate flow for new projects.
3. **Multi-model**: roles mapped to models per profile, with fallbacks and a residency
   manager that keeps loads within the machine's memory.
4. **Multi-agent**: agents defined as files (Markdown with YAML frontmatter), run as an
   orchestrator with workers that get fresh, narrow contexts, communicating only through
   the orchestrator with typed results, scheduled within the memory budget.

lokal exposes these through three interfaces only: an interactive terminal mode that works
over SSH, a headless mode that streams JSON events for other programs (Agent-Control,
scripts, CI), and the existing OpenAI-compatible gateway and MCP server for external
tools. lokal has no web UI; that is Agent-Control's job.

Every capability ships as default only where an evaluation on the target profile shows
it helps (coding task set, single-agent versus multi-agent on the same models).

## Consequences

### Positive
- One runtime under every feature: skills, retrieval, policy, traces, and the forge work
  the same in a quick question, a coding session, and a multi-agent run.
- Traces from coding and multi-agent runs are the richest input the forge can get.
- External tools keep working; lokal adds local models, knowledge, and skills to them.

### Negative
- A much larger scope: the roadmap grows from eight to ten milestones, and coding and
  multi-agent features are where small local models struggle most. On a CPU-only host the
  experience will be slow; evaluations will show it rather than hide it.
- A terminal interface is a product surface to maintain.

### Neutral
- The agent definition format and the JSON event stream become stable surfaces at v1.0.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Keep lokal to setup, gateway, RAG, and skills; leave agents to other tools | Other tools do not share lokal's skills, traces, and review gates, so the forge would have nothing to learn from |
| Build a web UI for coding and chat | Duplicates Agent-Control and needs a browser path to a headless VM |
| Free-form multi-agent conversations (agents chatting peer to peer) | Hard to bound and to debug, and costly in context for small models; orchestrator-workers first |
| A lokal-specific agent definition format | Same reasoning as ADR-0004: Markdown with frontmatter is already common for agent and skill definitions |

## Follow-up

- [ ] Owner accepts or amends this ADR
- [ ] M5: context engine and session design notes before code
- [ ] M6: edit-format and sandboxing decisions recorded as ADRs at milestone start
- [ ] M8: agent definition schema recorded as an ADR at milestone start
