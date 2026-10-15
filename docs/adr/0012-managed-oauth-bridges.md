# ADR-0012: Managed OAuth bridges and account model discovery

- **Date:** 2026-10-15
- **Status:** Accepted

## Context

Requiring users to supply bridge endpoints and two environment credentials made
browser sign-in fail before login could start. An account name and provider choice
should be enough to prepare the local OAuth transport. Available models should
come from the authenticated account rather than a manually entered list.

## Decision

Extend ADR-0011 with a managed default for Antigravity and Codex OAuth. Generate
independent cryptographic transport keys and allocate a loopback port per account
inside the locked account-store mutation. Store key references separately from
metadata. Keep explicit external bridge configuration available for operators.

Download the official CLIProxyAPI v8.0.29 release into the runtime home with
platform-specific pinned SHA256 digests. Validate bounded archives, reject unsafe
entries, and compare cached executables with the verified archive before execution.
No shell commands, package-manager install or administrator access is required.
Use portable no-plugin releases on Linux and leave Brain's static builds intact.

The gateway owns its managed child processes; account operations and direct client
launches can borrow a running authenticated bridge or start a temporary one.
Use account-specific private directories and OS process locks, restricted child
environments, and explicit lifecycle cleanup. No process is signalled through a
PID read from disk. Disable the bridge's plugins, management panel and discovery.
The OAuth broker owns provider tokens and refresh; native client sessions remain
separate. Configuration edits still require a gateway restart.

After verifying the bound identity, request the credential's model API rather than
a global catalog. Persist discovered models separately from the routing model
list. Refresh automatic lists while retaining explicit or legacy model limits.
Model discovery does not prove remaining quota or successful inference for every
listed model. Expose cached lists and explicit refresh through the account CLI.

Recover incomplete earlier external bindings when the user repeats default
onboarding with the same provider/name. Explicit conversion also supports existing
bindings. Preserve the account ID and leave externally owned sessions intact.

## Alternatives considered

| Option | Reason rejected |
|---|---|
| Keep environment keys and URLs mandatory | Makes normal browser onboarding depend on infrastructure knowledge |
| Share one bridge and auth directory across accounts | Cannot guarantee that account selection uses only the requested identity |
| Install the latest executable without a reviewed digest | Changes the executable trust boundary without a reproducible version |
| Use package-manager services | Platform-specific, may need elevated privileges, and mixes process ownership |
| Present all vendor models as account entitlements | A generic catalog does not establish what the credential exposes |
| Replace all model restrictions during refresh | Unexpectedly widens explicitly configured routing limits |

## Consequences

The first OAuth connection needs an executable download, and each active identity
uses a bridge process. Maintainers must deliberately update pinned releases and
verify their command flags and API contracts. Offline/custom deployments can use
external mode. Windows runtime validation and actual two-identity consent/model
integration remain deployment checks; no native primary-account isolation is
inferred from managed gateway transport.
