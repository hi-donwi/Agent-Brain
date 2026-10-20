# ADR-0013: The platform owns agent policy contracts; the host executes

- **Date:** 2026-10-20
- **Status:** Accepted
- **Deciders:** Doni Wicaksono
- **Amends:** ADR-0008 (the boundary stays; the platform's set of read-only contracts grows)

## Context

ADR-0008 made Brain the local AI platform and left the agent loop, tool execution,
approvals, coding sessions, and multi-agent to agent hosts, chiefly Agent-Control. It also
stated that Brain's MCP tools are read-only and that Brain never writes to a repository or
runs a command in it.

Brain now serves one routing pool of cloud accounts as well as local models, and the owner
wants it to be capable behind a coding agent: it should know a project's instructions,
decide which tool calls are permitted, and define sub-agents. Each agent host would
otherwise re-implement those three things in its own language, and they are exactly where a
mistake is a security defect: an instruction file from a repository is untrusted text, a
permission check that mishandles a path or a chained shell command is an escape, and a
sub-agent that can exceed its parent is a privilege escalation.

Go and TypeScript cannot share a library (ADR-0008). What can be shared is a contract:
file formats and a decision procedure with test vectors.

## Decision

Brain owns three **read-only agent policy contracts**, implemented in `internal/agent`:

1. **Project instructions.** Discovery of `AGENTS.md`, `CLAUDE.md`, and `GEMINI.md` from the
   project root down to the working directory, one file per directory, bounded in size,
   confined to the root, rendered as guidance that states it grants no permission.
2. **Tool permissions.** A policy of allow, ask, and deny rules over seven tools (read,
   search, edit, write, shell, network, subagent). The most restrictive matching rule wins.
   Paths are judged under a root with symbolic links resolved. Shell commands are split at
   separators and every part is judged; command substitution and redirection never
   auto-allow. A policy a repository ships is an **untrusted layer**: its allow rules are
   dropped and it can only tighten. Anything the policy cannot place or understand is
   denied or asked, never allowed.
3. **Sub-agents.** Definitions are Markdown files with YAML frontmatter. A sub-agent's
   effective policy is its parent's restricted to the tools it lists, so it can never
   exceed its parent. Omitting the tool list means read and search only. A sub-agent
   cannot start another unless it lists the subagent tool. Definitions are loaded from
   directories in trust order; the earlier directory wins and the clash is reported.

The agent host remains the only party that executes a tool call. It asks Brain what is
permitted and then enforces the answer under its own approvals and isolation. Brain does
not write to a repository, run a command in it, or hold the agent loop. ADR-0008's rule
that MCP tools are read-only still holds: the decisions are computed, not performed.

Model choice is not part of the contract. A sub-agent definition may name a model, and the
host picks the default per session.

## Consequences

### Positive
- One reviewed, tested implementation of the parts where a bug is an escape; hosts in any
  language reuse the formats and the test vectors.
- A repository cannot grant itself permission, replace the operator's sub-agent, or widen
  a sub-agent beyond its parent.
- The boundary of ADR-0008 is kept: no second agent loop, no second approval system.

### Negative
- Brain's scope grows from knowledge and routing to include a policy engine, which needs
  its own maintenance and security review.
- A permission decision is only as good as the host's enforcement. Shell prefix rules are
  not a sandbox: a denied command written in an unusual way falls to ask, not deny. Hosts
  must still isolate execution in a worktree or container.
- Until the exposure follow-up lands, Agent-Control cannot call these contracts.

### Neutral
- Instruction files and sub-agent definitions follow conventions other tools already use,
  so a project works with several agents unchanged.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Brain runs the full agent loop and tools (reverse ADR-0008) | The owner chose not to; duplicates Agent-Control's shipped loop, approvals, and worktree sessions |
| Leave policy to each host, per ADR-0008 as written | Each host re-implements path and shell checks; the likeliest place for a divergent, unsafe copy |
| Treat a repository's policy file as trusted | A cloned repository could grant itself shell or network access |
| Match shell commands with a regular expression allow-list | Easy to bypass with chaining and substitution; prefix tokens plus a fail-closed split is auditable |

## Follow-up

- [x] Stage 1: `internal/agent` with instructions, permissions, and sub-agent registry, tested
- [ ] Expose the three contracts as read-only MCP tools and a `brain agent` inspection
      command, so a host and an operator can query them
- [ ] Publish the policy and definition formats with shared test vectors for Agent-Control
- [ ] Agent-Control: adopt the contracts in place of its own checks (its own roadmap)
- [ ] Revisit a Brain-hosted loop only if evaluations show hosts cannot use these contracts
