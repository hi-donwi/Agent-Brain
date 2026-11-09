# ADR-0014: Profile-scoped inference authorization

- **Date:** 2026-11-08
- **Status:** Accepted
- **Deciders:** Project maintainer through the Agent-Control integration contract
- **Project:** Agent-Brain

## Context

The gateway currently accepts its master token or a token pinned to one provider account.
An account token does not constrain model, protocol, or the complete set of eligible
upstream endpoints. Agent-Control needs a credential that can be approved per project
without receiving the gateway master token or provider credentials. Authorization must
remain valid only for the exact routing targets that were reviewed.

## Decision

We define routing profiles in the private `accounts.yaml` router configuration. Each
profile maps an exact caller-visible model ID to one provider, upstream model, supported
protocol set, and explicit candidate account IDs; local targets use the configured local
backend and have no account IDs. The profile revision is `sha256:<hex>` over canonical
profile data and the resolved remote target identities, including provider, account ID,
and effective base URL, but excluding credentials and dynamic health. Brain issues an
HMAC-signed inference credential scoped to one profile ID, revision, and protocol set.
Every scoped request must present the matching `X-Brain-Profile` and
`X-Brain-Revision`; Brain resolves the profile, restricts model discovery, and routes only
through its declared target/account set. Agent-Control additionally requires the same
profile/revision in the selected project's operator egress policy. Changing a profile's
target or revision invalidates its existing credential and requires fresh project policy
approval. Rotating the master gateway token revokes all derived credentials.

For remote targets, the revision includes the resolved provider, account ID, and effective
base URL. For local targets it includes the selected backend name and loopback endpoint;
backend selection is deterministic. API credential values and dynamic health are excluded.

## Consequences

### Positive
- Project authorization is bound to immutable, content-addressed target sets.
- Agent-Control receives no Brain master token or provider credential.
- Account failover remains inside the profile's explicit account set.
- Profile-scoped model discovery reveals only that profile's model IDs.

### Negative
- Profile and endpoint policy revisions must be updated together after a target change.
- Derived credentials cannot be individually revoked; rotating the master token revokes all
  of them, and removing/changing a profile invalidates credentials for that revision.
- Profiles are edited in Brain's private router configuration and take effect after restart.

### Neutral
- Existing master-token access remains available for local administration and legacy clients.
- Hardware/runtime profiles remain separate from routing profiles.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Reuse account-scoped HMAC tokens | Pins only an account and leaves model/protocol selection broad. |
| Sign arbitrary profile text without resolving it server-side | Does not bind a profile to actual upstream accounts or endpoints. |
| Use the local hardware profile | It is unrelated to provider routing and cannot describe cloud targets. |
| Put the master token in Agent-Control and rely only on its policy | A compromised host could bypass project-level profile restrictions at Brain. |
| Persist every issued token for individual revocation | Adds credential storage and lifecycle administration beyond this integration slice; master rotation and profile revision changes provide bounded revocation. |

## Follow-up

- [x] Define the profile/revision and gateway authorization contract.
- [x] Add profile validation, resolution, routing enforcement, and negative tests.
- [x] Add scoped credential issuance and document rotation/revision behavior.
- [x] Integrate exact profile/revision checks in Agent-Control operator policy.
