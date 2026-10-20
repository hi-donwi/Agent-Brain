# Agent policy contracts

Status: Stage 1 implemented in `internal/agent` (see [ADR-0013](../adr/0013-platform-owns-agent-policy-contracts-host-executes.md)).
Not yet exposed through MCP or the command line; see Follow-up.

An agent host that wants Brain behind a coding agent needs three answers: what the project
asks of the agent, whether a tool call is allowed, and which sub-agents exist and what each
may do. This document defines the formats and the decision rules. The host executes tool
calls; Brain only describes and decides.

## 1. Project instructions

Discovery walks from the project root down to the working directory. In each directory the
first usable file of `AGENTS.md`, `CLAUDE.md`, `GEMINI.md` is read, in that order, so a
`CLAUDE.md` that only points at `AGENTS.md` is not read twice.

| Rule | Value |
|---|---|
| Order of results | Root first, nearest last; nearer files take precedence |
| Per-file cap | 32 KiB, cut on a character boundary and marked truncated |
| Total cap | 96 KiB; over the cap, the nearest files are kept and the rest reported |
| Working directory | Must be inside the root, after symbolic links are resolved |
| Skipped and reported | Not a regular file; a symbolic link resolving outside the root; unreadable |

The rendered text says the files guide style and workflow, grant no tool permission, and
cannot override the operator's policy.

## 2. Tool permissions

### Tools

`read`, `search`, `edit`, `write`, `shell`, `network`, `subagent`. Any other name is denied.

### Policy document

```yaml
default: ask            # allow | ask | deny; ask when omitted
rules:
  - tool: read
    path: "**"          # glob, relative to the root; read, search, edit, write only
    decision: allow
  - tool: edit
    path: "secrets/**"
    decision: deny
  - tool: shell
    command: "go test"  # token prefix; shell only
    decision: allow
```

Unknown fields, tools, or decisions are an error. A `path` must be relative and contain no
`..`. In a glob, `*` matches within one path segment, `**` across segments, `?` one
character.

### Decision rules

1. Among the rules that match a request, the most restrictive decision wins
   (deny over ask over allow). Rule order does not matter.
2. A request no rule matches gets `default`.
3. A path is placed under the root with symbolic links in its existing part resolved. A path
   outside the root, or any path when the policy has no root, is denied.
4. A shell command is split at `;`, `&`, `|`, and newlines, and every part is judged; the
   result is the most restrictive part. The split ignores quoting on purpose: it can make a
   command look like more commands, never fewer. `git status` matches `git status -s` and
   not `git statusx`.
5. A command containing `$(`, a backtick, `<`, or `>` is never better than ask.

Deny rules on shell commands are guardrails, not a sandbox. A forbidden command written in
an unusual way falls to ask, not allow, but the host must still isolate execution.

### Layering

`Layer(trusted, untrusted)` combines the operator's policy with one a repository ships. The
untrusted layer's allow rules are dropped, and its default applies only when stricter.

## 3. Sub-agents

A definition is `<name>.md` with YAML frontmatter and a Markdown prompt:

```markdown
---
name: reviewer
description: Reviews a diff for correctness and tests
tools: [read, search]       # allow-list; omit for read and search only
model: antigravity/claude-sonnet-4-6   # optional
max_turns: 12               # optional; default 20, at most 50
---
You review diffs. Report findings with file and line.
```

| Rule | Value |
|---|---|
| Name | Lowercase letters, digits, hyphens; starts with a letter; equals the file name |
| Required | `name`, `description`, a non-empty prompt |
| Tools omitted | `read`, `search` |
| Tools `[]` | No tools |
| Unknown field or tool | Error |
| File size | At most 32 KiB; symbolic links are not followed |

**Effective policy:** the parent's policy restricted to the listed tools. It can never be
wider than the parent's. A sub-agent cannot start another unless `subagent` is listed.

**Registry:** `LoadRegistry(dirs...)` takes directories from most to least trusted. For a
name defined twice, the earlier wins and the clash is reported, so a repository cannot
replace the operator's own role. A bad file is reported and does not stop the others.

## Follow-up

- Read-only MCP tools and a `brain agent` inspection command exposing the three contracts.
- Shared test vectors so a host in another language can check its implementation.
- Per-role model routing, once the owner chooses a default (today the host picks per session).
