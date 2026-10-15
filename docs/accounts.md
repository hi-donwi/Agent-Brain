# Accounts and provider connections

## Interactive and scripted setup

Run `brain accounts add` in a terminal. Choose a provider ID, authentication
method, and account name. At the API-key prompt, paste the key directly; terminal
input is hidden. Brain saves it in a separate private credential file and keeps
only a file reference in `accounts.yaml`. This works for Anthropic, OpenAI,
Google AI Studio and every other API-key provider. Keys must be shorter than
4096 bytes. `env:NAME` and absolute `file:PATH` references remain supported.
For scripts, use `--key-stdin` to supply a literal key without a command-line
argument. Piped input without required flags returns an error instead of opening
the wizard.

The wizard lists provider prerequisites before starting authentication. Choosing
OpenCode asks for its underlying provider. API-key setup shows the provider's key
management page; it does not present that page as an OAuth authorization URL.

`accounts list --json` is a redacted metadata view. `configured` means a record
exists; it does not prove upstream authentication. `accounts test` resolves the
credential and checks model discovery, or verifies a native client session.
`--probe --model MODEL` explicitly sends a minimal inference request and may
incur usage. Native client readiness and gateway readiness are independent.

Model IDs are discovered automatically after OAuth login and when a newly added
API/local account has an available credential. The wizard does not require model
IDs. List cached discovery results with `brain accounts models provider/name`, or
fetch them again with `brain accounts models provider/name --refresh` (`--json`
is available). Explicit `--models` restrictions remain in place during refresh.
Catalog entries describe models exposed for that credential; a listing does not
prove remaining quota or successful inference for every model.

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

Antigravity and Codex OAuth gateway connections use a private CLIProxyAPI v8
bridge per identity. Automatic setup is the default:

```sh
brain accounts add antigravity --name work
brain accounts add antigravity --name personal
brain accounts add codex --auth oauth --name work
```

In the interactive wizard, choose the provider, authentication method and account
name. Brain generates separate local inference/management credentials, chooses
an unused loopback port, and prepares an independent private auth directory.
There are no bridge URL, environment-key or model-ID prompts in this path.
Brain opens the browser or prints the login URL with `--no-browser`; the user
completes the provider's consent. After login, Brain verifies the bound identity,
queries `/credentials/models?name=<bound-id>`, saves and displays the model IDs.
It never substitutes a general provider catalog for that credential's catalog.

The bridge registers account models asynchronously after sign-in and after its
startup HTTP endpoint becomes reachable. Brain automatically polls that same
credential's catalog for up to 60 seconds during add/login, model refresh and
account verification, keeping its temporary bridge alive throughout the wait.
A progress message explains the wait. Authentication/configuration/identity errors
stop immediately rather than being retried as catalog delays. If the wait expires,
Brain retains the authenticated identity without reporting gateway readiness;
retrying add/login resumes that binding without requesting a new browser login.
A genuinely empty provider catalog still requires checking that account's provider
access or connectivity; Brain never fills it with unrelated static model IDs.


The first use downloads **CLIProxyAPI v8.0.29** from its official GitHub release
into the Brain runtime home (about 24 MB). Platform-specific SHA256 digests are
pinned in the installer, checked before extraction, and checked again when using
the cached archive. The executable is compared with that verified archive on
reuse. No package manager or administrator access is needed. Supported targets
are macOS/Linux/Windows on amd64 and arm64; Linux uses the portable no-plugin
build. Files stay under `tools/cliproxyapi/` and `bridges/` inside the runtime home.
An initial offline setup can use the external-bridge option below.

`brain serve` starts and owns the enabled managed bridges, and stops its processes
when the gateway shuts down. Login, test, model refresh and `cli --direct` borrow
a matching running bridge or own a temporary process for their command. Each
managed bridge has an OS process lock, an independent auth directory, disabled
plugins/control panel/network discovery, and an environment that omits provider,
storage and management credentials from the parent shell. Ports and secrets are
local transport details; the bridge still owns OAuth tokens and their renewal.
Brain does not import an existing native CLI keyring session.

If an earlier onboarding attempt saved an incomplete external account, rerun
`brain accounts add` with the same provider/name to resume automatically. To
explicitly convert an existing external connection, use:

```sh
brain accounts login antigravity/work --managed-bridge
brain accounts models antigravity/work --refresh
```

Conversion retains the Brain account ID and leaves the external bridge and its
credentials untouched. `--no-login` prepares the account without downloading or
starting a bridge. It can be connected with `accounts login` later.

### Advanced: an externally operated bridge

Use `--external-bridge` when the operator already runs CLIProxyAPI v8 and wants
to retain control of its processes and credentials. Brain never starts or stops
that external service. Each external account requires a dedicated instance with:


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
brain accounts add antigravity --external-bridge --auth oauth --name work \
  --base-url http://127.0.0.1:8317 \
  --bridge-url http://127.0.0.1:8317/v8/management \
  --key env:BRAIN_BRIDGE_WORK_KEY \
  --bridge-key env:BRAIN_BRIDGE_WORK_MANAGEMENT_KEY --no-browser
```

Brain obtains an authorization URL from the bridge, validates its HTTPS host and
state, prints it, and optionally opens a browser. It polls the same login state,
then verifies the sole credential's provider/identity and model discovery.
Cancellation attempts to cancel that pending bridge OAuth session.

Repeat with another dedicated external instance, port, auth directory, keys, and
account name for a second identity. The adapter also accepts the `codex` provider. A
separate official Sign in with ChatGPT application integration is not enabled
without application registration and client configuration.

For an SSH deployment, URL-only login still needs a route for the browser's
OAuth callback. Forward the callback port shown in the authorization URL as
well as any bridge port accessed from the local machine. Antigravity's bridge
callback uses port 51121; for example, establish
`ssh -L 51121:127.0.0.1:51121 user@server` before completing that browser flow.
Keep the tunnel open until login verification finishes.

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

Cached discovery metadata is separate from an explicitly restricted routing
model list. A refresh updates automatically discovered routing lists while
preserving explicit or legacy model restrictions.

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
- [CLIProxyAPI pinned release](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v8.0.29)
- [CLIProxyAPI management API v8](https://help.router-for.me/management/apiv8)
