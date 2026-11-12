# ADR-0023: Single-file Brain-user skill import

- **Date:** 2026-11-11
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0019 (adds a narrowly typed Brain-user skill install action)

## Context

Brain's unscoped Settings page can manage Brain-user skills, but installation remained CLI-only.
Accepting a path or ZIP archive through the browser would create filesystem traversal, symlink,
archive-bomb and overwrite risks. Skill instructions are also active data for future agents and
may contain secrets or prompt-injection text.

## Decision

The Brain-origin console may install one text `SKILL.md` file into Brain's owner-only user skill
directory. The request includes a safe skill slug, exact matching SKILL.md frontmatter name, body
up to 64 KiB, and exact `install <slug>` confirmation. Brain reuses the existing parser/validator
and Forge secret/prompt-safety scanner. Existing names cannot be overwritten. The final write is
staged in a private temporary directory on the same filesystem and committed with exclusive
directory creation and a hard-link of the validated owner-only file; partial/error paths leave no
active skill. Project/configured skills, ZIPs, references and arbitrary paths stay CLI-owned.

The UI warns that installed instructions are untrusted and become available to Brain clients.
Import content remains in the Brain-origin frame and does not transit Agent-Control's server or
configuration.

## Consequences

### Positive

- Operators can install a simple Brain-user skill without a filesystem path or Agent-Control
  secret handling.
- Validation, safety scanning, no-overwrite and owner-only atomic installation are enforced by
  Brain.

### Negative

- This import supports one file only; references, assets, and archives remain CLI-only.
- Skill instructions can affect future Brain clients after installation, so explicit confirmation
  and user trust remain essential.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Accept arbitrary source path | Lets the browser request host filesystem reads and copies. |
| Accept ZIP with folder tree | Requires safe archive extraction, symlink and zip-bomb handling beyond this surface. |
| Overwrite existing skill | Can silently change active agent instructions and lose prior content. |

## Follow-up

- [ ] If archive/reference import is needed, define a separately bounded archive schema and
      extraction contract; never loosen this endpoint to arbitrary paths.
