# ADR-0011: Provider accounts and native CLI boundaries

- **Date:** 2026-10-15
- **Status:** Accepted
- **Deciders:** Project maintainer through feature approval; implementation agent
- **Project:** Agent-Brain

## Context

An API-key pool and a directory named after a CLI profile cannot establish the
identity of a browser login or provide inference access to that session.
Antigravity, AI Studio, Claude subscription login, Anthropic API billing,
ChatGPT/Codex login, and OpenAI API billing have different authentication
contracts. Some requested clients also require different wire protocols.

The implementation must retain local inference and its existing interfaces,
build without CGO for the release targets, protect credentials, and support both
interactive and scripted account setup without silently choosing another account.

## Decision

Use one account service for validation, provider-scoped names, immutable IDs,
protected credential references, defaults, lifecycle state, and atomic
cross-process mutations. Keep secrets out of JSON metadata. A static capability
catalog declares authentication and supported protocols; custom endpoints are
explicitly configured and unrelated custom origins require an account pin.

Delegate native login to official unmodified clients with documented storage
bindings. Claude uses separate configuration directories; Codex uses a separate
child-process home and official file credential storage. Cursor supports distinct
user API keys; its browser session is exposed only as the default native session.
Do not infer primary Antigravity Google identity isolation from folder names or
unverified environment variables.

Use account-scoped HMAC tokens for routed client launches. Forward actual
Responses, Messages, chat, embeddings, and Gemini payloads only to compatible
accounts. Keep local inference orchestration available with explicit local-only,
cloud-only, and combined serving modes. The existing protocol-compatible error
envelope remains part of this compatibility boundary.

For optional Antigravity/Codex gateway OAuth, integrate with a separately operated
local CLIProxyAPI management API v8. Require one dedicated identity, no other
upstream API-key groups, plugins disabled, and matching management/inference
roots. Recheck the bound identity before forwarding. The external bridge owns
tokens and renewal. The application does not import native session tokens.

Reuse the already-pinned `go-isatty` module directly for portable terminal
detection; no new dependency version is introduced. Numbered terminal prompts
and the flag path share the account service.

## Consequences

### Positive

- Several providers can use the same account display name without overwriting.
- Scoped client credentials restrict account selection and protect the master token.
- The local AI platform remains useful independently of cloud services.
- Native login and optional gateway access have independently verifiable readiness.
- Existing IDs, the `gemini` alias, and legacy home-directory behavior remain readable.

### Negative

- Native client configuration and external bridge support require contract tests
  and maintenance as upstream versions change.
- Each OAuth gateway identity requires a separate local bridge instance.
- Running gateways need a restart after account configuration changes.
- OS file permissions do not replace Windows directory access-control policy.
- Native Antigravity primary-account switching and isolated Cursor browser
  profiles remain unavailable until their mechanisms are verified.

### Neutral

- OAuth consent and real account/model entitlement remain provider/user actions.
- A direct official Sign in with ChatGPT application flow requires registration
  and is separate from the optional bridge integration.
- Metadata discovery establishes available catalog entries, not model entitlement.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Treat every Google login as an AI Studio API key | Credentials, billing, services, and quotas are different |
| Set every client's guessed config variable | Does not prove identity isolation and can preserve the wrong account |
| Forward native subscription session tokens as generic API credentials | Violates service boundaries and bypasses the native client's storage contract |
| Use one shared OAuth bridge for account pins | A bridge can choose a different credential without an established selection contract |
| Run a coding agent's print mode as model inference | Agent tools and execution permissions differ from an inference API |
| Silently fall back when the gateway/account is unavailable | Changes identity, destination, and potentially billing without a matching user choice |

## Follow-up

- [x] Implement the account service, routing boundaries, wizard, and client bindings.
- [x] Document actual support and conditional integration prerequisites.
- [x] Add regression and protocol contract tests.
- [ ] Verify real OAuth identities and native clients on each deployment's installed versions.
