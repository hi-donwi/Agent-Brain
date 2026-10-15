# Multi-provider accounts and interactive onboarding

Status: Approved for implementation on 2026-10-15. These commands describe the target behavior, not
features already implemented. No authentication integration is accepted merely
because a profile directory exists or a child process exits successfully.

## Objective and assumptions

Manage several accounts for each provider through one account registry. A user
can select an account when launching a coding CLI and use eligible accounts as
inference backends through the Brain gateway. Adding an account should guide the
user through provider selection and the appropriate authentication flow.

Antigravity account sessions and Google AI Studio API keys are separate provider
identities. Sharing a Google identity or a Gemini model name does not make their
credentials, model access, billing, or quotas interchangeable.

Assumptions:

- The CLI is the initial interface; a web account dashboard is outside this feature.
- Existing local inference, RAG, MCP, and legacy home-directory behavior remain supported.
- Browser consent remains a user action. Brain can open a browser or display a
  login URL; it cannot complete consent on the user's behalf.
- Native CLI authentication and gateway authentication may require separate
  connections. Reuse is permitted only after a versioned integration proves it.
- Provider selection is explicit when several services expose the same model.

## Provider and authentication catalog

Providers declare supported authentication methods, account types, model
discovery, native CLI launch, gateway protocols, refresh, and revocation. The
wizard exposes only capabilities supported by the installed integration.

| Provider ID | Authentication | Native CLI | Gateway |
|---|---|---|---|
| `antigravity` | Google account sign-in through a validated native or bridge adapter | `agy`; account selection must be verified | Separate Antigravity adapter; not the AI Studio endpoint |
| `google-ai-studio` | API key, preferably an environment or secret-store reference | API-key mode only where explicitly supported | Gemini API and its documented OpenAI compatibility endpoint |
| `claude-code` | Claude subscription sign-in delegated to the unmodified official CLI | `claude auth login`; separate configuration per account | Subscription login is not offered as a general inference credential; use the `anthropic` API integration |
| `codex` | ChatGPT browser/device sign-in | Official `codex login` flow; verified account isolation | Evaluate the official Sign in with ChatGPT integration with its own registered client and eligible inference access |
| `openai` | API key | Existing launcher, with verified configuration | OpenAI API |
| `anthropic` | API key | Existing launcher, with verified configuration | Anthropic Messages API |
| `cursor` | Cursor browser login or Cursor user API key | Official Cursor agent login; verify account isolation | Conditional on a documented compatible inference service; the coding agent is not a model endpoint |
| `opencode-zen` | OpenCode service API key | OpenCode client | Verify the service's documented model/protocol endpoints before enabling |
| `deepseek` | API key | Compatible clients | OpenAI-compatible API |
| `openrouter` | API key | Compatible clients | OpenAI-compatible API |
| `local` | None or endpoint credential as required | Compatible clients | Configured local backend |

Preserve `gemini` as an input alias for `google-ai-studio`. Never map it to
`antigravity`. Google Vertex AI and Gemini CLI account sign-in are distinct
integrations and must not appear as supported until their own adapters pass tests.
Likewise, an OpenAI API key is not a Codex subscription login, and an Anthropic
API key is not a Claude subscription login.

OpenCode also has a client role independent of OpenCode Zen. A user choosing
OpenCode in onboarding selects an underlying provider or its own service, then
Brain configures the OpenCode client for that account. Do not create a fictional
OpenCode OAuth credential for every provider OpenCode can use.

Additional OpenAI-compatible providers can use an explicit custom-provider
registration with endpoint, supported protocols, model IDs, and credential
method. Compatibility still requires contract tests. Provider registration is
deliberate; an unknown name must never implicitly create an adapter.
Candidate additions include Gemini CLI, GitHub Copilot, Azure OpenAI, Vertex AI,
Groq, and xAI. They use the same capability contract and enter the supported
catalog only after official authentication and protocol checks pass.

### Client compatibility targets

The table records integration targets and evidence to use during implementation,
not completed Brain support.

| Client | Login entry point | Account isolation | Brain gateway integration |
|---|---|---|---|
| Antigravity | Official CLI's Google sign-in | Must pass the Antigravity gate below | Separate adapter and protocol tests |
| Claude Code | `claude auth login` | Official docs describe `CLAUDE_CONFIG_DIR` per account | Anthropic API credential and Messages protocol; preserve native subscription mode separately |
| Codex | `codex login`; device flow when enabled | Verify the credential-store namespace per account | Responses API support is required; changing `OPENAI_BASE_URL` alone is insufficient |
| OpenCode | `opencode auth login`; provider-specific methods | Isolate actual auth data as well as configuration | Explicit custom provider configuration, model map, and local gateway credential |
| Cursor agent | Official Cursor `agent login` or verified `cursor-agent` alias | Verify the installed client's supported mechanism | Only supported documented endpoint overrides; do not assume generic OpenAI environment variables apply |
| Aider / Continue | Provider or endpoint configuration | Per-client configuration as documented | OpenAI-compatible connection after contract verification |

Detect each executable's identity and version. The name `agent` is ambiguous;
never launch an unrelated binary or an editor launcher as the Cursor agent.
Unavailable and unverified capabilities have explicit reasons in
`brain providers list` and are disabled in the wizard.

## Interactive commands

`brain accounts add` without a provider opens the wizard on an interactive
terminal. `brain accounts add <provider>` starts with that provider selected.

```text
$ brain accounts add
Provider:
  Antigravity
  Anthropic / Claude
  OpenAI / ChatGPT / Codex
  Google AI Studio
  Cursor
  OpenCode
  DeepSeek
  OpenRouter
  Local endpoint

Authentication: Browser sign-in
Account type: Personal
Account name: work
Connect for: CLI and gateway

Opening your browser. Complete the provider's sign-in and consent.
Login URL: <URL returned by the authentication adapter>
Waiting for authentication...

Account: antigravity/work
CLI connection: authenticated
Gateway connection: action required
Next: brain accounts login antigravity/work --for gateway
```

The example deliberately shows a partial connection: report both connections as
authenticated only when both are verified. If the integration supports connecting
both in one flow, complete that flow and show both verified outcomes.

Wizard rules:

1. Select the provider and an authentication method from its capability catalog.
   Provider-family labels can group subscription/native login and API usage, but
   the resulting provider identity and billing mode remain explicit. For example,
   choosing Claude subscription delegates to the official Claude CLI, while
   choosing Anthropic API creates an API account.
2. Offer personal/business account types only when the integration supports them;
   request a cloud project or location only for flows that require them.
3. Choose a display name; allow `work` under several providers without overwriting.
4. Select CLI, gateway, or both where supported. Explain an unavailable capability
   before starting a login, including any required CLI or local bridge.
5. For browser sign-in, start the adapter's login flow, show the returned URL, and
   open the browser when available. `--no-browser` prints the URL. Use a device code
   only if that provider actually offers a device authorization flow.
6. For API keys, accept an environment reference or a hidden terminal input. Offer
   the provider's official key-management page; generating a key is a separate
   provider action. Never present that page as an OAuth login URL.
7. Verify the authenticated identity and connection capabilities before declaring
   success. A remote model probe must be explicit because it can incur usage.
8. Show the resulting account, CLI/gateway readiness, and next command. Offer to
   add another account or select a provider default.

Cancellation, denial, expiration, callback errors, and browser-open failures have
clear recovery actions. Failed login attempts do not replace a working account
or become active accounts. On noninteractive stdin, missing required flags return
an actionable error without prompting or hanging.

```sh
# Browser flow; repeat with another account name.
brain accounts add antigravity --auth oauth --name work --for both
brain accounts add antigravity --auth oauth --name personal --for both --no-browser

# API-key flow; retains a reference instead of printing or embedding a secret.
brain accounts add google-ai-studio --auth api-key --name work --key env:GEMINI_WORK_KEY
brain accounts add google-ai-studio --auth api-key --name personal --key env:GEMINI_PERSONAL_KEY

brain accounts list --provider antigravity
brain providers list
brain accounts use antigravity/work
brain accounts login antigravity/work --for gateway --no-browser
brain accounts test antigravity/work
brain cli --account antigravity/work agy

# Other native login flows, where the installed adapter supports them.
brain accounts add claude-code --auth native --name work --for cli
brain accounts add codex --auth native --name work --for cli
brain accounts add cursor --auth native --name work --for cli --no-browser
brain cli --account claude-code/work claude
brain cli --account codex/work codex
brain cli --account openai/work opencode
```

`accounts use` chooses a default within its provider. `accounts test` checks
credentials and adapter readiness without sending a prompt; `--probe` explicitly
requests a minimal remote inference check. Administrative metadata and JSON
output never contain raw keys, access/refresh tokens, or secret header values.

## Account model and persistence

Separate immutable account identity, display name, provider, authentication method,
credential reference, routing preferences, and verified capabilities. Store names
under a provider namespace and resolve canonical references such as
`antigravity/work` to an opaque internal ID; never turn the reference directly
into a filesystem path.

Track `pending`, `authenticated`, `reauth_required`, `disabled`, and independent
CLI/gateway readiness. Rate limits are temporary routing state, scoped to the
account and relevant model/quota group. A configured account is not proof of a
working credential. A user-entered tier is a routing preference, not proof of a
paid subscription or an API entitlement.

Use protected credential storage and keep secret values out of ordinary account
metadata. Environment references remain supported and must resolve before use.
Native Claude subscription credentials remain entirely under the official
client's control; Brain stores a profile binding, not copied OAuth tokens.
OAuth refresh is coordinated per account and persisted atomically; removal and
logout distinguish deleting a Brain entry from revoking an upstream session.
Writes must not lose another process's changes. Reject traversal and symlink
escapes and create credential directories/files with appropriate private modes.

Read legacy `accounts.yaml` without silently changing its meaning. Preserve
existing IDs when unambiguous; normalize the `gemini` alias, detect duplicates,
and require explicit resolution of ambiguous legacy references. Never silently
merge CLI profile folders into authenticated account entries or discard raw
legacy credentials during migration.

## Routing and launcher behavior

- A provider adapter owns URLs, credential injection, protocol translation, and
  supported models. Unknown providers fail before any upstream network call.
- Provider-qualified model references select a service explicitly, for example
  `antigravity/<model>` and `google-ai-studio/<model>`. Resolve an account
  independently; account IDs must not masquerade as model IDs in `/v1/models`.
- Preserve unqualified legacy model inference where unambiguous. Conflicting
  defaults require an explicit route rather than silently selecting a service.
- `brain cli --account ...` must select the same registry entry for native launch
  and routed requests. For the gateway, use an authenticated, account-scoped local
  routing context; resolve and validate it server-side. A profile directory alone
  does not pin a gateway account.
- Pinning is strict. Cross-account fallback requires an explicit opt-in. Pool
  routing can use priority or round-robin among eligible accounts of the selected
  provider, honoring cooldowns and a bounded retry/deadline policy that can reach
  accounts beyond the first three. Respect `Retry-After` and avoid repeated
  attempts to an already failed candidate within a request.
- Cross-provider fallback is separately configured. Never substitute AI Studio
  for Antigravity or local inference for an unsupported provider.
- Strip local gateway credentials, account selectors, and hop-by-hop headers
  before injecting upstream credentials. Do not forward user-supplied query keys
  as an alternative to the selected account credential.
- Gateway startup rejects invalid account configuration. Changes become visible
  through a validated reload or a clearly reported restart requirement.
- The Codex integration includes `/v1/responses` and its streaming/tool-call
  behavior before routed Codex support is advertised. Each client must pass its
  actual wire protocol, not only a basic chat-completions mock.
- Tool-specific configuration uses only verified flags/environment variables.
  Do not inject every tool's configuration variables into every child process.
  Preserve caller arguments after an explicit `--` delimiter.

## Antigravity integration gate

The official CLI documentation describes OS-keyring sign-in and a separate
Gemini API-key mode. It does not establish that assigning a Brain profile name
isolates the primary Google session or exposes it as an inference API.

Before accepting the adapter, prove with two user-authorized test identities:

1. Adding the second identity preserves the first identity and the normal CLI login.
2. Selecting either account launches `agy` as the intended primary identity, and
   simultaneous sessions cannot overwrite each other's credentials.
3. Gateway authentication, refresh, model discovery, streaming, and routing use
   the selected Antigravity identity, with no dependency on an AI Studio key.
4. Logout/removal of one account preserves the other; unsupported versions return
   a precise capability error.

Prefer a documented native integration when available. Otherwise evaluate a
pinned, separately configured local OAuth bridge such as CLIProxyAPI; its own
documentation describes Antigravity login URLs and multi-account inference.
That is a third-party adapter, not an official Google API. Its credential store
is not automatically the official CLI's keyring. Validate each connection and
do not claim that bridge authentication also signs in the native CLI.

Do not implement an inference adapter by launching an unrestricted coding agent
in the caller's repository. Headless CLI output represents an agent run with
tools and execution permissions, not automatically a model inference API.

If no integration passes both identity and gateway requirements, report the
specific unsupported capability. A partially working integration cannot satisfy
the feature's completion criteria.

## Other provider integration constraints

Claude native multi-account launch can use the documented per-account
configuration mechanism and `claude auth login`. Native subscription login must
complete inside the unmodified official binary. Anthropic's documentation does
not permit offering Claude.ai login as a third-party inference authentication
method or intermediating those session tokens; expose API-key or supported
cloud-provider authentication for Brain inference instead.

For ChatGPT plan usage, prefer OpenAI's documented Sign in with ChatGPT flow
where the app and account are eligible. Obtain a client ID for this application
and implement the documented inference, renewal, and attribution requirements.
Do not substitute an extracted Codex refresh token for application registration.
Native Codex login remains a separate, independently verifiable integration.

Cursor documents browser login, URL-only login through `NO_OPEN_BROWSER=1`, and
its own user API keys. These establish native authentication, not an entitlement
to arbitrary OpenAI-compatible inference. Verify Cursor gateway support
separately. Keep API keys issued by Cursor distinct from OpenAI/Anthropic keys.

OpenCode accepts custom provider configuration including a base URL and model
map. Configure Brain there explicitly; setting generic process environment
variables without verifying that OpenCode uses them is insufficient. Apply the
same rule to additional clients and provider plugins.

## Implementation structure and style

Keep Cobra prompts and flags in `internal/cli`, lifecycle/authentication workflows
in an account service, provider capabilities and adapters in `internal/router` or
a dedicated provider package, and HTTP authentication/routing in
`internal/gateway`. Keep the provider catalog as the single source for validation,
wizard choices, and help output. Secret storage must not be the public response DTO.

Use the existing Go 1.26+, Cobra v1.10.2, constructor-based commands, injected I/O,
contexts, and `gofmt`. Reuse one account service for interactive and flag-only
paths. Keep prompts and diagnostics separate from machine-readable stdout; use
English text without emoji. A terminal or keyring dependency needs a concrete
compatibility/security rationale and a pinned version before adoption.

## Testing strategy and acceptance criteria

Write failing behavior tests before each implementation slice. Unit tests use
fake providers, browsers, clocks, transports, credential stores, and command I/O.
Integration tests use loopback mock OAuth and inference servers, never personal
credentials. Cover state/PKCE validation where supported, denial/cancellation,
refresh races, missing environment keys, redaction, migration, identity pinning,
protocol correctness, and concurrent account updates.

The feature is complete only when:

- Interactive and scripted onboarding share validation and produce equivalent records.
- Two Antigravity accounts and two AI Studio accounts coexist without replacing
  each other; identical display names across providers are safe.
- Claude native, Codex native, Anthropic API, OpenAI API, OpenCode client, and
  Cursor native integrations each have account-isolation and login contract
  coverage. Two identities must coexist for each integration claimed supported.
- `brain providers list` reports per-provider authentication, multi-account,
  native-launch, gateway, and protocol capabilities accurately; conditional
  integrations cannot appear fully supported.
- Native CLI selection and gateway selection each use the intended account.
- Browser and URL-only login flows verify identity and readiness accurately.
- No credentials appear in account listings, HTTP metadata, diagnostics, or logs.
- Provider/model routing, cooldown, and account fallback pass contract tests.
- All Antigravity integration gates above pass; mocks alone are insufficient.
- `make check` passes. Run `go test -race ./internal/router ./internal/gateway
  ./internal/cli` for relevant concurrency changes and `make static-check` if a
  new dependency can affect the existing static, cross-platform distribution.

Always preserve existing data and test failure behavior. Obtain approval of this
specification before implementing the new account/authentication architecture;
obtain separate authorization for any live browser login or account revocation.
Never read or copy a user's existing credential stores as a test fixture, embed
secrets in source, install an external bridge silently, or mark an unverified
integration ready.

## Sources

- [Antigravity CLI installation and authentication](https://antigravity.google/docs/cli/install)
- [Antigravity CLI reference](https://antigravity.google/docs/cli/reference)
- [Antigravity headless mode](https://antigravity.google/docs/cli/headless)
- [Gemini API OpenAI compatibility](https://ai.google.dev/gemini-api/docs/openai)
- [Claude Code authentication and multiple accounts](https://code.claude.com/docs/en/authentication)
- [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference)
- [Claude Code authentication and credential use](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use)
- [Codex authentication](https://learn.chatgpt.com/docs/auth)
- [Sign in with ChatGPT](https://developers.openai.com/siwc)
- [Codex app-server with ChatGPT plan access](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)
- [OpenCode providers and custom endpoints](https://opencode.ai/docs/providers/)
- [Cursor CLI authentication](https://cursor.com/docs/cli/reference/authentication)
- [CLIProxyAPI Antigravity integration](https://help.router-for.me/configuration/provider/antigravity)
- [CLIProxyAPI management API](https://help.router-for.me/management/apiv8)

External references checked on 2026-10-15. Verify the installed integration's
version and behavior again before implementation.
