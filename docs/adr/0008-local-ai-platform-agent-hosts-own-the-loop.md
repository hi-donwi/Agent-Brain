# ADR-0008: lokal is the local AI platform; agent hosts own the agent loop

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono
- **Supersedes:** ADR-0007

## Context

ADR-0007 gave lokal its own agent runtime, a coding agent, and multi-agent orchestration.
It was accepted before a close look at the agent host that already exists in the same
family. Agent-Control is the agent host of the Agent-Workspace framework (framework
ADR-0019): a TypeScript runtime that owns LLM calls, the agent loop, tool execution, local
approvals, coding sessions in isolated worktrees, sub-agents, and the web console. That
stack is shipped and tested; lokal has no agent code yet.

Keeping ADR-0007 would mean one maintainer building and securing two agent loops, two
approval systems, and two coding-session implementations in two languages. Moving
Agent-Control's stack into lokal instead would break framework ADR-0019, carry cloud
protocols and workspace governance into a tool meant to be standalone and local-only
(ADR-0006), and rewrite tested code with no new value for users.

What no agent host in the family provides is the local layer underneath: setting up
engines and models that fit the machine, guarding against silent truncation (ADR-0002),
keeping several models within memory when several agents call at once, local retrieval,
skills, and learning new skills from real work. Agent-Control's own documentation points
its local endpoint at Ollama's OpenAI-compatible API, which the M0 spike showed truncates
prompts silently past the loaded context.

## Decision

lokal is the **local AI platform** that agents use. It owns:

1. **Models:** setup and doctor, the OpenAI-compatible gateway with roles, fallbacks,
   residency within the memory budget, request queueing, the token guard, and a token
   counting endpoint so agent hosts can budget their own context.
2. **Knowledge:** local retrieval with citations, and code knowledge (repository map,
   symbol and code search, project instruction files), served over MCP.
3. **Skills:** the Agent Skills runtime and router, served over MCP.
4. **Learning:** a versioned trace format, opt-in trace capture at the gateway, importers
   for agent-host session logs, a minimal bounded runner used only for evaluations, and
   the skill forge (ADR-0005).

The agent loop and everything around it belong to **agent hosts**: Agent-Control for
Agent-Workspace operators (loop, tools, policy and approvals, coding sessions, vibe
coding, multi-agent, web UI), and existing agents (opencode, Aider, Continue, Cline, and
other tools that speak the OpenAI API or MCP) for everyone else. They use lokal through
the gateway and MCP.

lokal's MCP tools are read-only. lokal never writes to a user's repository or runs a
command in it; every write and exec happens in the agent host, under its approvals. The
eval runner works only in throwaway directories.

## Consequences

### Positive
- One agent stack in the family, one platform under it; no duplicated security surface.
- Every agent host, not only one, gets local models that do not truncate silently or run
  out of memory, plus local knowledge and skills.
- lokal stays standalone and local-only; Agent-Workspace remains optional.
- A shorter roadmap: nine milestones instead of ten, none of them an agent product.

### Negative
- lokal alone is not a code assistant; users without Agent-Workspace need an existing
  agent. The docs must say so plainly.
- Small-model techniques that live inside a loop (edit formats per model, compaction) are
  the agent host's job; lokal can only measure them (M6 eval sets) and help from below.
- The forge depends on traces from outside: gateway capture sees model traffic, and
  richer session context needs an importer per agent host.

### Neutral
- The boundary is a set of protocols (OpenAI API, MCP, trace format), so moving the loop
  later remains possible if evidence asks for it.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Keep ADR-0007: lokal builds its own runtime, coding agent, and multi-agent | Duplicates Agent-Control's shipped stack in a second language for one maintainer |
| Move Agent-Control's agent stack into lokal; Agent-Control becomes a UI | Breaks framework ADR-0019, imports cloud protocols and workspace governance into a standalone local tool, rewrites tested code for no user value |
| A new shared repository for the duplicated parts | Go and TypeScript cannot share a library; the shared parts are contracts, which live with their producer. Revisit if a third consumer appears |
| Merge Agent-Local and Agent-Control | Different audiences, languages, and deployment; the framework would become a dependency of a general tool |

Revisit moving the loop into lokal only if evaluations show agent hosts cannot work well
with local models even with lokal's help, if the framework makes the agent host
language-neutral, or if lokal users need a built-in agent that existing agents do not
serve.

## Follow-up

- [x] ADR-0007 marked superseded
- [ ] M2: token counting endpoint; residency designed for concurrent agent load
- [ ] M5: code knowledge tools over MCP (read-only)
- [ ] M6: trace format, gateway capture, Agent-Control session-log importer, eval runner
- [ ] Agent-Control (its own roadmap): lokal endpoint preset through a tunnel, MCP client,
      session-log export or documented format
