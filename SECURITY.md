# Security

Agent-Local runs models, reads your files and code, and records traces of how agents use
them, so security is part of the product, not an add-on.

## Reporting a vulnerability

Please do **not** open a public issue for security problems. Report them privately and
include the version (`lokal version`), the affected command, and reproduction steps you
can share safely.

- Open a private security advisory:
  `https://github.com/hi-donwi/Agent-Brain/security/advisories/new`

You will receive an acknowledgement, and we will coordinate a fix and a disclosure
timeline with you. Reporters are credited unless they prefer otherwise.

## Security properties (target for v1)

These are design commitments recorded in [ADR-0005](docs/adr/0005-learned-skills-require-human-promotion.md),
[ADR-0006](docs/adr/0006-local-only-by-default.md), and
[ADR-0008](docs/adr/0008-local-ai-platform-agent-hosts-own-the-loop.md); each is
implemented and tested in the milestone named in the [roadmap](ROADMAP.md).

- **Loopback only.** The gateway binds to `127.0.0.1` and requires a per-process bearer
  token stored in a file only the user can read. The MCP server uses stdio.
- **No egress, no telemetry.** lokal talks only to configured local backends and to model
  registries during an explicit setup or pull.
- **Read-only toward your code.** lokal's MCP tools never write to a repository or run a
  command in it; agent hosts do that under their own approvals. `lokal eval` works only
  in throwaway directories it creates.
- **Content is data.** Retrieved documents, tool output, and traces are never treated as
  instructions.
- **Redaction.** Strings matching known credential patterns are redacted before a trace
  is written.
- **Learned skills cannot activate themselves.** Skill candidates carry provenance and an
  evaluation report and are used only after a human promotes them.

## Supply chain

Every push runs govulncheck and the Agent-Secure gate (gitleaks and osv-scanner at pinned,
checksum-verified versions). GitHub Actions are pinned by commit SHA.
