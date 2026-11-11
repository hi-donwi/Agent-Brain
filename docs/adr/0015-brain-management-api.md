# ADR-0015: Brain management API for trusted local operator UI

- **Date:** 2026-11-10
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Project:** Agent-Brain

## Context

Agent-Control currently routes inference through profile-scoped credentials, while
Brain account and provider administration is CLI/configuration-only. A profile
credential must not become an account-management credential, and giving Agent-Control
the gateway master token would bypass its least-privilege boundary. Provider secrets
must remain owned and stored by Brain.

## Decision

Brain exposes a separately authenticated `/manage/v1/` API. Management credentials
are explicitly issued by the Brain CLI, signed from the gateway master secret, stored
in an owner-only file, and accepted only by management routes. They are distinct from
the master and inference/account-scoped credentials; rotating the master invalidates
them. The management API returns redacted account/provider data and never returns
resolved provider or bridge credentials. Mutations reuse Brain's validated account
service and private secret storage. Credential inputs are write-only.

Agent-Control embeds a Brain-origin management UI; the management API is same-origin
only. The browser must use the configured Brain endpoint and a management credential
entered in that Brain-origin page; neither is copied to Agent-Control configuration
or local storage. Brain still validates its host header. Agent-Control's CSP permits
frames only from explicitly configured Brain endpoints, and Brain permits framing
only from the supported local Agent-Control origins.

The contract covers provider capability discovery; account list/add/update/remove/default;
credential checks/model discovery without inference; and asynchronous OAuth login sessions
with explicit status and cancellation. OAuth account creation uses Brain-managed isolated
bridges only. Native login remains in the official local client/Brain CLI when it requires
an interactive terminal. Config writes report that a running gateway needs restart; the
management API does not execute arbitrary commands or silently restart services.

## Consequences

### Positive

- Agent-Control's profile-scoped chat token cannot manage accounts.
- The gateway master token is never sent to Agent-Control.
- Provider credentials remain stored in Brain's private credential directory.
- Brain's CLI and HTTP management use the same validation and persistence logic.

### Negative

- The operator must issue and enter a separate management credential.
- Native CLI login, service lifecycle, and Brain-local RAG/skills/trace/evaluation
  operations remain outside the account API contract.
- Brain account changes require gateway restart before inference routing sees them.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Reuse the profile-scoped inference token | It intentionally cannot inspect accounts or administer provider state. |
| Give Agent-Control the gateway master token | A compromised host would gain unrestricted gateway authority. |
| Proxy management requests through Agent-Control backend | Provider secrets would pass through and become available to the host process. |
| Allow arbitrary CLI commands over HTTP | It creates remote command execution and bypasses typed validation. |

## Follow-up

- [x] Implement and document authenticated management endpoints and exact origin policy.
- [x] Add Agent-Control provider/account management entry point using the Brain-origin UI.
- [x] Add typed OAuth login, cancellation, credential check, and model-discovery flows.
- [ ] Add other Brain capability APIs only after separate threat-model and contract review.
