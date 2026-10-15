# Accounts and provider connections

## Interactive and scripted setup

Run `brain accounts add` in a terminal. Choose a provider ID, authentication
method, and account name. API-key accounts accept `env:NAME` or an absolute
`file:PATH` reference. Use `--key-stdin` for a literal key stored privately by
Brain. Piped input without required flags returns an error instead of opening
the wizard.

The wizard lists provider prerequisites before starting authentication. Choosing
OpenCode asks for its underlying provider. API-key setup shows the provider's key
management page; it does not present that page as an OAuth authorization URL.

`accounts list --json` is a redacted metadata view. `configured` means a record
exists; it does not prove upstream authentication. `accounts test` resolves the
credential and checks model discovery, or verifies a native client session.
`--probe --model MODEL` explicitly sends a minimal inference request and may
incur usage. Native client readiness and gateway readiness are independent.

Use canonical references such as `openai/work`. Short names are accepted only
when unambiguous. Existing account IDs remain valid. Repeated provider/name
creation fails, and names cannot contain filesystem traversal components.

## Native CLI connections

Claude Code uses its documented `CLAUDE_CONFIG_DIR` mechanism. Codex uses a
distinct child-process `CODEX_HOME` and its official file credential-store option.
The clients own their login credentials; Brain stores a profile binding and
checks authentication status without importing their session tokens.

Cursor API-key accounts use the selected `CURSOR_API_KEY`. Cursor browser login
is available only for `cursor/default`, because independent native browser-session
storage has not been established. `--no-browser` uses `NO_OPEN_BROWSER=1`.
Brain identifies Cursor through its explicit client executable rather than
assuming that any program named `agent` is Cursor.

Native clients must already be installed. Missing clients produce a setup error.
Use `--no-login` to create a pending account and run `accounts login` later.
Cancelled or failed login never marks an account ready. Claude subscription
login remains inside the unmodified official client; Brain does not expose it
as a generic third-party inference credential.

## Antigravity and Codex OAuth gateway connections

The OAuth adapter targets CLIProxyAPI's **management API v8**, separately
installed and operated on loopback. It is an optional third-party integration.
Brain does not install it, reuse an existing native keyring session, or copy its
OAuth tokens. The bridge owns authentication and token renewal.

Each account requires a dedicated bridge instance with:

- A separate port and auth directory.
- Exactly one ready OAuth credential for the selected provider after login.
- An empty auth directory before creating a new login.
- `config-version: 8`, no upstream `api-keys` groups, and plugins disabled.
- Separate inference and management keys, supplied to Brain through references.
- Management and inference URLs for the same instance. Brain requires the
  management URL to be its inference root followed by `/v8/management`.

Configure and start the bridge following its
[official configuration guide](https://help.router-for.me/configuration/options).
An illustrative local configuration, with placeholders replaced in a private
file, is:

```yaml
config-version: 8
server:
  host: 127.0.0.1
  port: 8317
management:
  allow-remote: false
  secret-key: "<management-secret>"
access:
  api-keys: ["<inference-secret>"]
oauth:
  auth-dir: "./auth-work"
api-keys: {}
plugins:
  enabled: false
```

Set `BRAIN_BRIDGE_WORK_KEY` and `BRAIN_BRIDGE_WORK_MANAGEMENT_KEY` to the matching
local secrets, then connect:

```sh
brain accounts add antigravity --auth oauth --name work \
  --base-url http://127.0.0.1:8317 \
  --bridge-url http://127.0.0.1:8317/v8/management \
  --key env:BRAIN_BRIDGE_WORK_KEY \
  --bridge-key env:BRAIN_BRIDGE_WORK_MANAGEMENT_KEY --no-browser
```

Brain obtains an authorization URL from the bridge, validates its HTTPS host and
state, prints it, and optionally opens a browser. It polls the same login state,
then verifies the sole credential's provider/identity and model discovery.
Cancellation attempts to cancel that pending bridge OAuth session.

Repeat with another dedicated instance, port, auth directory, keys, and account
name for a second identity. The adapter also accepts the `codex` provider. A
separate official Sign in with ChatGPT application integration is not enabled
without application registration and client configuration.

```sh
brain accounts test antigravity/work
brain serve --cloud-only
brain cli --account antigravity/work opencode
```

Every inference request rechecks the bound bridge identity. A bridge whose
credential changes or whose configuration gains other inference identities is
rejected. Brain will not silently switch a pinned request to another account.

For the official Antigravity CLI as a routed client, merge this documented entry
into its existing `~/.gemini/antigravity-cli/settings.json` yourself:

```json
{"modelProvider": "gemini"}
```

Then `brain cli --account antigravity/work agy --model MODEL` injects the local
gateway URL and an account-scoped token. This setting selects the CLI wire
protocol; the upstream credential remains the chosen Antigravity OAuth account.
AI Studio keys continue to belong to `google-ai-studio` accounts. Independent
native Google sign-in profiles in `agy` remain unverified and are not advertised.

## Routing and storage

`accounts use` selects a default within that provider. Several provider defaults
matching a client require an explicit `--account`. `router strategy priority`
and `router strategy round-robin` control unpinned pools. Rate limits honor
cooldowns and `Retry-After`; each eligible candidate is attempted at most once
per request. Unsupported protocols and missing credential references fail
without substituting another service. Different custom endpoint origins require
an explicit account selection.

Changes become active after restarting a running gateway. `accounts disable`
and `accounts remove` change Brain configuration; they do not revoke provider
credentials or delete native client sessions. Log out through the official
client or the external bridge to clear those sessions.

Metadata is in `accounts.yaml`; new literal credentials live separately in
`secrets/`. Directories use mode 0700 and files mode 0600 where the OS implements
those permissions. Windows relies on the runtime directory's inherited access
control; keep it inside a private user profile. Environment references avoid
persisting literal secrets. Permission-protected files are not an encrypted
credential vault; use references backed by your existing secret manager when
encrypted secret storage is required. Mutations take a cross-process lock and replace the
metadata file atomically. After an interrupted writer, inspect a remaining
`accounts.yaml.lock` before removing a stale lock manually.

Legacy `gemini` identifiers normalize to AI Studio. Existing IDs and raw legacy
credential values remain readable, while all JSON views omit credential fields.
Malformed configuration fails gateway startup instead of becoming an empty pool.

## Source contracts

- [Antigravity authentication](https://antigravity.google/docs/cli/install)
- [Claude native multiple accounts](https://code.claude.com/docs/en/authentication)
- [Anthropic credential-use boundaries](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use)
- [Codex authentication](https://learn.chatgpt.com/docs/auth)
- [Codex provider configuration](https://learn.chatgpt.com/docs/config-file/config-reference)
- [Cursor browser and API-key login](https://cursor.com/docs/cli/reference/authentication)
- [OpenCode provider configuration](https://opencode.ai/docs/providers/)
- [CLIProxyAPI management API v8](https://help.router-for.me/management/apiv8)
