# Gateway account and protocol contracts

The default root is `http://127.0.0.1:52140`. `/healthz` is unauthenticated;
other endpoints require a gateway credential. The gateway retains the existing
OpenAI-style JSON error envelope with `message`, `type`, and stable `code` fields.

| Method and path | Behavior |
|---|---|
| `GET /healthz` | Process health |
| `GET /v1/accounts` | Redacted runtime account metadata and cooldown state |
| `GET /v1/models` | Local model/role metadata and configured provider model IDs |
| `POST /v1/chat/completions` | Local chat or a compatible provider's raw chat protocol |
| `POST /v1/responses` | Raw Responses protocol for a compatible cloud or explicitly configured local endpoint |
| `POST /v1/messages` | Anthropic Messages or a bridge explicitly supporting that protocol |
| `POST /v1/embeddings` | Local embeddings or a compatible cloud provider |
| `POST /v1beta/models/{model}:generateContent` | Compatible Gemini protocol |
| `POST /v1beta/models/{model}:streamGenerateContent` | Compatible Gemini streaming protocol |
| `GET /v1beta/models` and `GET /v1beta/models/{model}` | Upstream Gemini model discovery using the selected account |
| `POST /v1/tokens` | Existing local token-count estimate |

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
The CLI launcher creates an HMAC-authenticated account-scoped token without
exposing the master token to its child. Scoped tokens cannot switch accounts
through another header or select a conflicting provider. Their account must
still exist and be enabled in the gateway's current configuration snapshot.

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
