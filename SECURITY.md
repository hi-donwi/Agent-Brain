# Security

Agent-Local runs models, reads your files, and (from M5) executes tools on your machine,
so security is part of the product, not an add-on.

## Reporting a vulnerability

Please do **not** open a public issue for security problems. Report them privately and
include the version (`lokal version`), the affected command, and reproduction steps you
can share safely.

- Open a private security advisory:
  `https://github.com/hi-donwi/Agent-Local/security/advisories/new`

You will receive an acknowledgement, and we will coordinate a fix and a disclosure
timeline with you. Reporters are credited unless they prefer otherwise.

## Security properties (target for v1)

These are design commitments recorded in [ADR-0005](docs/adr/0005-learned-skills-require-human-promotion.md)
and [ADR-0006](docs/adr/0006-local-only-by-default.md); each is implemented and tested
in the milestone named in the [roadmap](ROADMAP.md).

- **Loopback only.** The gateway binds to `127.0.0.1` and requires a per-process bearer
  token stored in a file only the user can read. The MCP server uses stdio.
- **No egress, no telemetry.** lokal talks only to configured local backends, model
  registries during an explicit setup or pull, and MCP servers you configured.
- **Approvals.** Agent tools that write files or execute commands ask first unless an
  explicit allow rule matches; file access is confined to the working directory.
- **Content is data.** Retrieved documents, tool output, and traces are never treated as
  instructions.
- **Redaction.** Strings matching known credential patterns are redacted before a trace
  is written.
- **Learned skills cannot activate themselves.** Skill candidates carry provenance and an
  evaluation report and are used only after a human promotes them.

## Supply chain

Every push runs govulncheck and the Agent-Secure gate (gitleaks and osv-scanner at pinned,
checksum-verified versions). GitHub Actions are pinned by commit SHA.
