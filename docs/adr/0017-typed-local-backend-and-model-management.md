# ADR-0017: Typed local backend and model management

- **Date:** 2026-11-10
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0016 (extends the narrow Brain-origin operator console to local runtime resources)

## Context

Agent-Control operators need to inspect Brain's local inference backend and manage its model
catalog without a terminal round trip. Backend configuration can contain bearer tokens, model
pull/delete have disk and availability effects, and model engines are independent HTTP services.
The Agent-Local/Brain architecture must not become a model runtime or general system installer.

## Decision

The Brain-origin console may manage only the currently configured typed runtime resources:

- Inspect redacted hardware, disk, backend, installed models, and embedded catalog state.
- Select only a backend entry already present in Brain's private configuration. Brain pings it
  before saving an exclusive selection; endpoint/token values are preserved, tokens are never
  serialized, and the running gateway is not hot-swapped.
- Select an embedded hardware profile only when all its role models are installed on the
  running backend. The change is config-only and requires a gateway restart.
- Pull only an exact embedded catalog model supported by the current Ollama adapter. Require a
  per-model confirmation, enforce the existing catalog size plus 2 GiB free-space margin, allow
  one bounded cancellable async job at a time, and expose progress without raw backend errors.
- Explicitly unload only a catalog model reported as resident. The residency manager blocks new
  gateway acquisitions, drains active requests, unloads through the backend adapter, and removes
  the residency record before reopening requests.
- Remove only an installed catalog model, after exact confirmation, only when no configured
  role/profile/local-account references it. The residency manager blocks new gateway acquisitions
  and drains active requests during deletion; Brain also refuses removal while the backend reports
  the model loaded.

Do not install/start/stop operating-system services, download arbitrary URLs, add or edit
backend endpoints/tokens through HTTP, run shell/CLI commands, or automatically restart the
gateway. Remote backend configuration changes require the existing deployment/tunnel and
operator trust boundary. Tests use fake adapters and never download models or contact the
authoring machine's inference services.

## Consequences

### Positive

- The operator can manage the current catalog/runtime without exposing credentials or arbitrary
  installation controls.
- Model downloads and removals have explicit confirmation, catalog bounds, disk protection, and
  role/in-use checks.
- Backend configuration remains the source of truth; restart requirements are visible.

### Negative

- This slice supports model pull/delete only for the Ollama adapter; other adapters remain
  inspectable but their external package/model lifecycle is CLI-owned.
- Pull job state is process-local and disappears on Brain restart; the job is cancelled during
  shutdown.
- Configured backend changes do not affect serving until the gateway is restarted.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Generic `brain setup` or shell invocation from HTTP | Executes arbitrary installation and hides side effects from the API contract. |
| Let the UI submit model tags or download URLs | Allows unreviewed pulls and bypasses catalog sizing/safety policy. |
| Hot-swap the running adapter | Existing requests/residency state may still use old backend configuration. |
| Let the UI install or stop system services | Service managers are platform-specific and require a separate system-level contract. |

## Follow-up

- [ ] Add typed service install/status only if the operator explicitly needs it and with a
      separate OS/service threat model.
- [ ] Consider safe model-pull support for other adapters only after each has a tested adapter
      contract and reliable size/disk metadata.
- [ ] Add remaining RAG, skills/MCP, trace/eval, and forge surfaces only through subsystem-
      specific APIs and data-boundary review.
