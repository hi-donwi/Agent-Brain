# Agent-Brain

Agent-Brain (`brain`) connects coding clients to local inference engines and cloud
AI services. It combines provider-aware account routing with local RAG, repository
intelligence, read-only MCP tools, traces, evaluation, and a skill forge.

Choose local inference, cloud accounts, or both. Several accounts can coexist for
each provider, with explicit account selection, priority or round-robin routing,
and account cooldowns. A local account token limits a launched client to its
selected account. Native CLI login remains separate from API billing.

## Build

Requires Go 1.26 or newer. Native coding clients are installed separately.

```sh
git clone https://github.com/hi-donwi/Agent-Brain.git
cd Agent-Brain
make build
./bin/brain version
```

Release build targets are macOS ARM64/AMD64, Linux ARM64/AMD64, and Windows AMD64.
The core binary uses pure-Go dependencies; Linux release binaries are statically
linked. Native clients and external inference engines have their own platform
requirements. The legacy `cmd/lokal` source entry point remains available.

## Add accounts

```sh
# Interactive: provider, authentication method, account name, and credential source.
./bin/brain accounts add

# Scripted: the same validation and account store.
./bin/brain accounts add openai --name work --key env:OPENAI_WORK_KEY
./bin/brain accounts add openai --name personal --key env:OPENAI_PERSONAL_KEY
./bin/brain accounts add anthropic --name work --key env:ANTHROPIC_WORK_KEY
./bin/brain accounts add google-ai-studio --name work --key env:GEMINI_WORK_KEY

./bin/brain accounts list
./bin/brain providers list
./bin/brain accounts test openai/work
./bin/brain accounts use openai/work
```

Set referenced environment variables in the processes that use the accounts,
including `brain serve`. For private file storage instead, pipe a key to
`accounts add ... --key-stdin`; Brain stores the value in a private credential
file and keeps a reference in account metadata. The interactive wizard accepts
`env:NAME` or `file:PATH` references and never echoes a literal key.

Account names are scoped by provider: `openai/work` and `anthropic/work` are
different identities. Adding an existing provider/name fails rather than
overwriting it. `gemini` remains an alias for `google-ai-studio`.

## Choose local, cloud, or both

```sh
# Local engine setup and model management.
./bin/brain setup --yes
./bin/brain models ls
./bin/brain serve --local-only

# Cloud accounts without initializing a local inference backend.
./bin/brain serve --cloud-only

# Combined local backend and configured provider accounts.
./bin/brain serve
```

Local backend adapters include Ollama, llama.cpp, vLLM, LM Studio, and MLX-LM.
Models run in their external engines. The gateway binds to loopback by default;
every inference endpoint requires authentication. State stays under `~/.lokal`
or the absolute path selected by `LOKAL_HOME` for backward compatibility.

For a separately managed local server, add an account with an explicit protocol:

```sh
./bin/brain accounts add local --name desktop --auth none \
  --base-url http://127.0.0.1:1234 --models local-model --protocols chat
./bin/brain accounts test local/desktop
./bin/brain cli --account local/desktop opencode
```

Declare `responses` only when that local endpoint implements the Responses API.
Use TLS or a loopback SSH tunnel for a server on another machine.

## Launch a client with an account

```sh
./bin/brain cli --account openai/work codex --model gpt-model-you-can-access
./bin/brain cli --account openai/work opencode
./bin/brain cli --account anthropic/work claude
./bin/brain cli --account cursor/work cursor
```

Run the gateway in another terminal before launching a routed client.
Gateway accounts require a running `brain serve`; `--direct` explicitly selects
the account's upstream endpoint instead. An unavailable gateway or invalid
account fails clearly. Without an account or matching default, the launcher
preserves the tool's native session. Put tool arguments after `--` when their
flag names overlap Brain's launcher flags.

OpenCode receives an explicit provider configuration and model map. Run
`accounts test` to discover models, or supply `--models` during account creation.
Codex receives an explicit Responses provider configuration. Incompatible client
and account protocols are rejected before launching.

## Provider and login support

| Integration | Account connection | Gateway behavior |
|---|---|---|
| Google AI Studio | Multiple API-key accounts | Gemini and OpenAI-compatible chat/embeddings |
| Antigravity | OAuth through a dedicated local CLIProxyAPI v8 instance per account | Separate Antigravity inference source; private bridge prepared automatically |
| Anthropic API | Multiple API-key accounts | Anthropic Messages |
| Claude Code subscription | Official `claude auth login` in separate profiles | Native CLI use; subscription credentials stay in the official client |
| OpenAI API | Multiple API-key accounts | Chat, Responses, and embeddings |
| Codex / ChatGPT | Official native login/device flow, or automatic private OAuth bridge | Native login and gateway credentials are separate connections |
| Cursor | Multiple Cursor user API keys; browser login for its default native session | Native Cursor client; no general Cursor model endpoint is assumed |
| OpenCode | Client configuration for an underlying account; explicit service endpoint support | Uses the selected provider/protocol |
| DeepSeek / OpenRouter | Multiple API-key accounts | Compatible chat API |
| Local / custom | Explicit endpoint, protocol, and optional credential | Only declared protocols; unrelated custom endpoints require an account selector |

```sh
./bin/brain accounts add claude-code --auth native --name work
./bin/brain accounts add codex --auth native --name personal --no-browser
./bin/brain accounts add cursor --auth native --name default --no-browser
./bin/brain accounts add cursor --auth api-key --name work --key env:CURSOR_WORK_KEY
./bin/brain cli --account claude-code/work claude
./bin/brain cli --account codex/personal codex
```

`--no-browser` prints the bridge login URL, uses the documented Codex device flow,
or sets Cursor's documented URL-only mode. Claude login delegates browser
handling to the official CLI. A browser consent step still requires the user.

Antigravity and AI Studio remain distinct even when both expose Gemini models.
Routed `agy` use requires its documented `modelProvider: "gemini"` client setting;
the selected Brain account determines the upstream service. Brain does not alter
global Antigravity settings or claim independent native Google keyring profiles.
OAuth setup automatically prepares the private bridge, local port and credentials;
complete the browser login to discover the account's models. No bridge URL or model
ID entry is needed in the default wizard.

```sh
./bin/brain accounts add antigravity --name work
./bin/brain accounts models antigravity/work --refresh
```

For an incomplete account from an earlier setup, rerun the wizard with the same
provider/name, or run `accounts login antigravity/work --managed-bridge`.
See the [account guide](docs/accounts.md) for automatic and external bridge modes.

## Other commands and verification

```sh
./bin/brain router status
./bin/brain router strategy round-robin
./bin/brain accounts disable openai/personal
./bin/brain accounts enable openai/personal
./bin/brain accounts remove openai/personal
./bin/brain doctor
./bin/brain mcp
./bin/brain index --help
./bin/brain search --help
./bin/brain forge --help
make check
make static-check
```

Account mutations require restarting a running gateway. `router status` reports
live account state when it can authenticate to that gateway and labels a
configuration snapshot otherwise. Listings and HTTP account metadata omit keys,
secret references, management credentials, and custom header values.

[Account guide](docs/accounts.md) · [Gateway contracts](docs/gateway-api.md) ·
[Architecture decision](docs/adr/0011-provider-accounts-and-cli-boundaries.md) ·
[Feature specification](docs/specs/multi-provider-accounts.md)

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
