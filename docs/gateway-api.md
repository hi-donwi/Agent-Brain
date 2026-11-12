# Gateway account and protocol contracts

The default root is `http://127.0.0.1:52140`. `/healthz` is unauthenticated;
other endpoints require a gateway credential. The gateway retains the existing
OpenAI-style JSON error envelope with `message`, `type`, and stable `code` fields.

| Method and path | Behavior |
|---|---|
| `GET /healthz` | Process health |
| `GET /v1/accounts` | Redacted runtime account metadata and cooldown state |
| `GET /v1/models` | Master token: normal model catalogue; profile token: only its authorized model IDs |
| `POST /v1/chat/completions` | Local chat or a compatible provider's raw chat protocol |
| `POST /v1/responses` | Raw Responses protocol for a compatible cloud or explicitly configured local endpoint |
| `POST /v1/messages` | Anthropic Messages or a bridge explicitly supporting that protocol |
| `POST /v1/embeddings` | Local embeddings or a compatible cloud provider |
| `POST /v1beta/models/{model}:generateContent` | Compatible Gemini protocol |
| `POST /v1beta/models/{model}:streamGenerateContent` | Compatible Gemini streaming protocol |
| `GET /v1beta/models` and `GET /v1beta/models/{model}` | Master/account token: upstream discovery; profile token: only its authorized Gemini targets |
| `POST /v1/tokens` | Existing local token-count estimate |
| `GET /manage/v1/providers` | Supported provider authentication/protocol capabilities; management credential required |
| `GET /manage/v1/accounts` | Redacted configured account metadata |
| `POST /manage/v1/accounts` | Add API-key/no-auth/native account or prepare an isolated Brain-managed OAuth account |
| `PUT /manage/v1/accounts/{id}` | Enable/disable, adjust priority/weight, or set an account default |
| `DELETE /manage/v1/accounts/{id}` | Remove account binding; does not revoke upstream provider credentials |
| `POST /manage/v1/accounts/{id}/test` | Verify API/OAuth credentials and refresh account models; never runs inference |
| `POST /manage/v1/accounts/{id}/models/refresh` | Refresh provider-bound model catalog; never runs inference |
| `POST /manage/v1/accounts/{id}/login` | Start/resume a bounded OAuth session and return its opaque session ID |
| `GET /manage/v1/accounts/{id}/login/{session}` | Read pending/completed/failed session status and validated provider URL |
| `DELETE /manage/v1/accounts/{id}/login/{session}` | Cancel the OAuth login and revoke the pending bridge state |
| `GET /manage/v1/profiles` | List routing profiles and resolved content revisions |
| `PUT /manage/v1/profiles/{id}` | Create or replace a validated routing profile |
| `DELETE /manage/v1/profiles/{id}` | Remove a routing profile |
| `GET /manage/v1/runtime` | Redacted hardware, disk, backend, and model-profile state |
| `GET /manage/v1/models` | Installed models on the running backend with safe catalog metadata |
| `GET /manage/v1/model-catalog` | Embedded catalog models/profiles supported by the running backend |
| `PUT /manage/v1/runtime/backend` | Select and verify an already-configured backend; restart required |
| `PUT /manage/v1/runtime/profile` | Select an embedded profile only when its role models are installed |
| `POST /manage/v1/model-pull-jobs` | Start a confirmed, catalog-only async model pull |
| `GET /manage/v1/model-pull-jobs` | List bounded in-memory model-pull job status |
| `GET /manage/v1/model-pull-jobs/{id}` | Read model-pull progress/status |
| `DELETE /manage/v1/model-pull-jobs/{id}` | Cancel a model-pull job |
| `POST /manage/v1/models/{catalog-id}/unload` | Explicitly unload a resident model after active requests drain |
| `POST /manage/v1/models/{catalog-id}/deletion` | Confirm and delete an installed catalog model when it is not assigned/loaded |
| `GET /manage/v1/knowledge` | List registered source and collection aggregate metadata; no paths/content |
| `POST /manage/v1/knowledge/index-jobs` | Reindex one Brain-registered source asynchronously |
| `GET /manage/v1/knowledge/index-jobs` | List bounded in-memory index jobs |
| `GET /manage/v1/knowledge/index-jobs/{id}` | Read index job progress/status without file paths |
| `DELETE /manage/v1/knowledge/index-jobs/{id}` | Cancel a reindex job |
| `POST /manage/v1/knowledge/search` | Search one registered collection and return citations/snippets to the Brain-origin console |
| `GET /manage/v1/skills` | List Brain-user skills only; omit filesystem paths |
| `POST /manage/v1/skills` | Validate/secret-scan and atomically install one confirmed `SKILL.md` in Brain-user home |
| `GET /manage/v1/skills/{name}` | Inspect one Brain-user skill's metadata and text body |
| `GET /manage/v1/skills/{name}/validation` | Validate one Brain-user skill and return failed field names |
| `DELETE /manage/v1/skills/{name}` | Confirm and remove one Brain-user skill directory |
| `GET /manage/v1/mcp` | Inventory read-only stdio MCP tools; no process start or tool invocation |
| `GET /manage/v1/traces` | Redacted trace aggregates with Brain-keyed pseudonymous IDs |
| `GET /manage/v1/evaluation-suites` | List the fixed local smoke suite |
| `POST /manage/v1/evaluation-jobs` | Run one bounded fixed-prompt smoke test on local Ollama |
| `GET /manage/v1/evaluation-jobs` | List bounded in-memory evaluation jobs |
| `GET /manage/v1/evaluation-jobs/{id}` | Read pass/assertion/token/time metadata without model output |
| `DELETE /manage/v1/evaluation-jobs/{id}` | Cancel an evaluation and clean its temporary sandbox |
| `GET /manage/v1/forge/candidates` | List opaque candidate IDs and gates; no candidate text or provenance IDs |
| `POST /manage/v1/forge/candidates/{id}/validation` | Validate structure and run secret-safety scan |
| `POST /manage/v1/forge/candidates/{id}/evaluation` | Run deterministic local fixtures with content-hash-bound report |
| `POST /manage/v1/forge/candidates/{id}/promotion` | Promote only reviewed, fresh-evaluated, owned-only candidates |
| `DELETE /manage/v1/forge/candidates/{id}` | Confirm and discard a Brain-local candidate |
| `GET /manage/v1/service` | Redacted launchd/systemd installed and gateway-health status |

Local engine orchestration retains its native adapter and residency behavior.
The adapter-based local chat path supports the existing text-message contract.
Structured/tool payloads use a compatible endpoint account; unsupported local
Responses requests return `protocol_not_supported` instead of invoking a coding
agent or silently changing protocols.

## Authentication and selection

`Authorization: Bearer TOKEN` works for every route. Native protocol clients can
also supply `x-api-key` or `x-goog-api-key`; Gemini routes accept `?key=TOKEN`.
These credentials authenticate to Brain and are removed before upstream calls.

The master token is stored privately in the runtime home's `serve.token` file.
The CLI launcher creates HMAC-authenticated account-scoped credentials without
exposing the master token to its child. They cannot switch accounts through another
header or select a conflicting provider; the account must remain in the current
configuration snapshot.

For Agent-Control, `brain router credential <profile-id> --protocol chat` writes a
separate owner-only profile-scoped credential. The token's signature binds one profile
ID, current `sha256` revision, and protocol set. Every inference or model-discovery call
must also carry matching `X-Brain-Profile` and `X-Brain-Revision` headers. Profile tokens
are Bearer-only, expose only model IDs authorized by that profile, cannot access account
metadata, and cannot select a different model, provider or account. The gateway checks
the revision against its current profile and resolved target endpoints on every request.
Rotating `serve.token` revokes all derived profile credentials; changing or removing a
profile invalidates credentials for its old revision.

Management uses a separate domain-separated credential. `brain router
management-credential` writes it to the owner-only `agent-control.management.token`
file (or a new file supplied by `--output`); it never prints the token and refuses
to overwrite an existing file. The credential is accepted only for `/manage/v1/`
routes and is denied on inference routes. Profile/account credentials are denied
from management routes. Rotating the master token revokes the management credential.
The operator transfers it from that private file directly into the embedded Brain-origin
console through a trusted local channel; it is never entered into Agent-Control code or
configuration, nor sent through the Agent-Control backend.

Management API requests with an `Origin` header must be same-origin; cross-origin
browser API calls are refused. The Brain-origin console is embedded by Agent-Control
only from explicitly configured endpoints. Non-browser clients may omit `Origin`,
but still require the management credential. Management responses are
`no-store`; account credential values and references, bridge secrets, backend tokens,
and raw credentials are never serialized. Account creation accepts a literal credential
in a write-only request field only. An `env:` or `file:` reference, in `credential_ref`
or as the credential value itself, is refused with `credential_reference_not_allowed`:
Brain would resolve it on the host and send the value to a base URL the same caller
chose. References are configured with the local CLI (`brain accounts add --key`). OAuth login is
supported for Antigravity/Codex accounts using isolated managed bridges; the browser sees
the validated provider authorization URL (including its protocol state), but no bridge
management credential or provider token. Native login
must run in its official local CLI; Brain does not launch a native CLI process from an HTTP
request. Account/profile mutations persist validated configuration
but do not reload a running gateway; account and profile changes become active
after restart. Profile revisions must be copied into the matching workspace project
egress policy before Agent-Control chat can use the new revision.

Local runtime management reports only the running adapter and configured backend
origins/IDs; endpoint userinfo, paths, query values, and tokens are omitted. Backend
selection is restricted to entries already in Brain's owner-only configuration, pings
the candidate before saving, preserves tokens, and requires a gateway restart. The
console cannot add arbitrary endpoints or install/start OS services.

Model pulls are supported only by the current Ollama adapter. They require an exact
embedded catalog ID and confirmation, must fit catalog download size plus the 2 GiB
safety margin, and run as one six-hour-bounded async job at a time. Job errors are
stable codes; backend text is not returned. Explicit unload blocks new residency
acquisitions and drains active requests before calling the backend. Model removal requires the exact catalog
ID confirmation, no active role/profile/local-account assignment, and a drained
residency lease so new gateway requests wait during deletion; it is also refused while
the backend reports the model loaded. Hardware-profile selection requires every role model already be installed;
profile changes are config-only and require restart. Tests use fake adapters and never
download a model or query an operator's live backend.

RAG source roots are registered locally with `brain knowledge source add`; canonical paths stay
in the owner-only Brain config. Management requests use only the registered source ID, never a
filesystem path or collection selector. Source listings and index job status omit paths,
filenames, document content, and adapter error text. Indexing/search use only an installed
catalog embedding model on the loopback Ollama backend; remote/cloud embedding is refused. The
walker skips symlinks and revalidates opened files under the canonical registered root. Reindex
jobs are single-active, four-hour-bounded, in-memory tasks; cancellation can leave already
committed documents updated, and a retry is incremental. Search results include bounded citation
snippets and are returned only to the same-origin Brain console; queries/snippets are not logged.
Registering/unregistering source roots remains a local Brain CLI operation.

Skills management is limited to Brain-user skills under the private user-skill directory. Project
`.agents/skills` and configured external sources are not returned to the unscoped console. Skill
bodies are untrusted text. Import accepts one `SKILL.md` body (64 KiB max), validates its name and
format, rejects Forge secret/prompt-injection scan findings, requires exact `install <name>`
confirmation, and never overwrites an existing skill. The file is atomically installed with
owner-only mode in Brain's user skill home. ZIPs, reference trees, arbitrary paths, and source
configuration remain CLI-owned. Removal also requires exact confirmation. MCP inventory
returns only static read-only tool metadata and `stdio`
transport. It does not report process arguments/status, start or stop a process, invoke tools, or
bypass per-project Agent Policy.

Trace inventory returns only aggregate counts, timestamps, models, token totals, duration and
error presence; session IDs are keyed aliases, and file paths/content remain local CLI-only.
The evaluation API accepts only the built-in `local-smoke` suite, an installed catalog model,
and exact confirmation. It uses a fixed synthetic prompt/assertion, the loopback Ollama backend,
and a disposable sandbox. It never accepts a task spec, setup files, `AgentCommand`, or shell
assertions. Job responses omit the prompt, output, and raw model error. This is one local smoke
check, not the full CLI evaluation runner or trace importer.

Forge inventory uses HMAC pseudonymous candidate IDs and omits names, descriptions, bodies,
paths, and source trace IDs. Candidate content/provenance review and `owned-only` classification
must be recorded locally with `brain skills review-candidate`; `brain forge candidates` maps local
names to the same opaque IDs. The unscoped API cannot review content on the user's behalf.
Promotion requires that review, structure/secret scan, and a fresh content-hash-bound passing
evaluation. External-content candidates remain CLI-only. Forge mining, drafting and hardening
against real traces are not exposed over HTTP.

`GET /manage/v1/service` reports only fixed service type, installed/running state, and optional
gateway version/profile. It never returns the local unit/plist path or raw service error. Service
install/uninstall remains CLI-owned (`brain service install|uninstall`): those operations mutate
OS launchd/systemd state, and uninstall can stop the gateway connection handling the request.
The current service helper also needs stronger command-error handling before HTTP lifecycle
mutations are considered.

A client authenticated with the master token can select a canonical account
through `X-Brain-Account: provider/name` or its immutable ID. Without a pin,
provider-qualified models select a pool, for example `openai/MODEL` and
`google-ai-studio/MODEL`. `X-Brain-Provider` is another explicit provider selector.
Conflicting selectors fail. Provider-qualified prefixes are removed from the
upstream model ID; the original protocol payload remains intact.

```sh
curl http://127.0.0.1:52140/v1/responses \
  -H "Authorization: Bearer $BRAIN_GATEWAY_TOKEN" \
  -H "X-Brain-Account: openai/work" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/MODEL","input":"Hello","stream":true}'
```

Profile callers use only the exact caller-visible model ID returned by filtered discovery;
account/provider selector headers are ignored and stripped:

```sh
curl http://127.0.0.1:52140/v1/chat/completions \
  -H "Authorization: Bearer $BRAIN_PROFILE_TOKEN" \
  -H "X-Brain-Profile: agent-control" \
  -H "X-Brain-Revision: sha256:<profile-revision>" \
  -H "Content-Type: application/json" \
  -d '{"model":"assistant","messages":[{"role":"user","content":"Hello"}]}'
```

Use the token from your private runtime file to populate the client environment;
do not put it in source control or logs. Account metadata never returns API keys,
credential references, secret headers, or bridge management credentials. Scoped
account metadata/model views exclude unrelated accounts.

## Compatibility and failure behavior

Provider adapters declare accepted protocols. No universal Anthropic/OpenAI/
Gemini translation is implied; an explicit external bridge can provide its own
translation. Native CLI subscription authentication does not make that session
a general inference credential.

Pinned requests never use another account. Unpinned requests try each eligible
account once, honoring provider/model restrictions and cooldowns. Missing
credentials, incompatible protocols, changed bridge identities, and unknown
providers fail without silently switching to local inference or another cloud
service. Status 401/402/403 exhausts an account; 429/529 establishes a cooldown.

Responses SSE, tool definitions, tool-result payloads, and structured cloud
messages pass through. Token compression only changes string content in tool
or function-result messages; other JSON fields retain their values. Request
bodies are limited to 16 MiB. Upstream redirects are not followed with account
credentials, and local credentials/hop-by-hop headers are stripped.

The local token-count endpoint remains an estimate, not a provider billing
counter. The upstream provider decides model entitlement and usage limits.
