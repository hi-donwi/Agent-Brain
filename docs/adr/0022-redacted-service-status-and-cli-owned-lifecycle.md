# ADR-0022: Redacted service status and CLI-owned OS lifecycle

- **Date:** 2026-11-11
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0016 and ADR-0017

## Context

Agent-Control Settings is the Brain-origin operator console, but its HTTP server runs inside the
Brain gateway service. `service.Install`/`Uninstall` write/remove launchd/systemd files and execute
OS service-manager commands; uninstall can stop the process serving the request, and current
helpers ignore some command failures. Returning `StatusResult` directly would also expose the
absolute unit/plist path and raw status message.

## Decision

Expose only a redacted `GET /manage/v1/service` summary: fixed service type, installed/running
booleans, and optional gateway version/profile. Never serialize unit/plist paths or raw OS error
messages. Keep install/uninstall and OS lifecycle commands in the local Brain CLI until their
side effects and command-error semantics are hardened and a reliable asynchronous response model
is designed. The Settings panel shows the exact local CLI commands, not HTTP mutation buttons.

## Consequences

### Positive

- The operator can understand service registration and gateway health in Settings without
  learning host paths or service internals.
- An unscoped management token cannot start/stop a launchd/systemd unit through an HTTP request.

### Negative

- Service installation/removal is not yet one-click in Agent-Control and requires the local CLI.
- Status is a point-in-time check; the gateway can change immediately after it is returned.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Expose raw `service.StatusResult` | Leaks absolute unit/plist path and raw service message. |
| Invoke install/uninstall inside request handler | Can terminate the handling service, and command failures are currently ignored. |
| Enqueue service stop and return accepted | The process can stop before the operator observes terminal job state; needs a durable external supervisor contract. |

## Follow-up

- [ ] Harden install/uninstall command error propagation and rollback before considering HTTP
      lifecycle actions.
- [ ] If adding async stop/restart, define job observation/recovery outside the service being
      stopped; do not use a generic shell runner.
