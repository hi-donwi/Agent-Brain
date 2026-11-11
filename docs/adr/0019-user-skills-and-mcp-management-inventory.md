# ADR-0019: User-local Skills and MCP inventory in the Brain console

- **Date:** 2026-11-10
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0016 (narrows user-facing Skills/MCP administration for an unscoped console)

## Context

Brain's Skills manager can see project, user, and configured-directory sources. Agent-Control
Settings is not bound to a current project, so exposing all of those sources could reveal skills
from another project/client. MCP's stdio server exposes read-only tools, some of which access a
project root under an Agent Policy. Invoking them from an unscoped management page would bypass
that per-project boundary.

## Decision

The Brain-origin management console may list, inspect, validate, and remove only Brain-user
skills under the owner-only `~/.lokal/skills` directory. Project `.agents/skills` and configured
skill source directories are excluded. Removal requires exact skill-ID confirmation and cannot
escape the Brain user-skill directory. The UI renders skill instructions as untrusted text and
does not execute them or include them in an agent prompt.

The console may display a static inventory of Brain's read-only MCP tool names/descriptions and
stdio transport. The MCP process remains CLI-owned (`brain mcp`); the management API does not
start/stop a process, invoke a tool, return project roots, or expose process arguments. Tool
calls remain under the MCP client's project/Agent Policy boundary.

## Consequences

### Positive

- An unscoped settings page cannot enumerate a project's or arbitrary configured skills.
- Installed Brain-user skills can be inspected, validated, and intentionally removed without a
  path argument or repository write.
- MCP tools remain read-only and are not invoked from the settings console.

### Negative

- Installing skills and launching the MCP stdio server remain local CLI actions.
- Project/configured skill source administration and project-scoped MCP invocation require a
  separately authorized, project-bound integration.

## Alternatives considered

| Option | Why rejected |
|---|---|
| List all sources returned by `skills.Manager` | The Settings page has no active project scope and could reveal other project instructions. |
| Accept a skill path/upload through HTTP | Creates arbitrary filesystem access and archive/path traversal risk. |
| Start MCP processes or invoke tools from management HTTP | Bypasses stdio lifecycle and per-project Agent Policy; may perform project reads outside scope. |
| Return MCP process args/status | Arguments may contain credentials and the process lifecycle is CLI-owned. |

## Follow-up

- [ ] Add broader Skills source/install lifecycle only with safe local source registration and
      project ownership boundaries.
- [ ] Add project-scoped MCP invocation only through the host's existing MCP client and Agent
      Policy, never through unscoped Settings.
- [ ] Remaining trace/evaluation and forge surfaces require their own data/side-effect review.
