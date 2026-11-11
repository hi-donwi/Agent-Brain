# ADR-0020: Safe trace inventory and local smoke evaluation

- **Date:** 2026-11-11
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0016 (extends the Brain-origin operator console with privacy-preserving trace/eval controls)

## Context

Brain trace records include user/client messages, model responses, tool arguments/results, and
errors. Agent-Control Settings is unscoped to one project. The evaluation runner supports custom
task specs and shell commands, even though its scratch directory is temporary; exposing these
inputs to a management credential would create arbitrary process execution and a prompt/data
exfiltration path.

## Decision

The unscoped Brain console may list trace aggregate metadata only: counts, times, model names,
token totals, duration, and error presence. Session IDs are returned only as HMAC aliases keyed
with the gateway master; trace file paths and all raw content are omitted. Trace detail/import/
delete remains CLI-owned.

The console may run one fixed `local-smoke` evaluation at a time. It uses a server-constructed
synthetic prompt/assertion, an already-installed embedded-catalog model, the loopback Ollama
adapter, a temporary sandbox, a 45-second task timeout, and a 32-token context bound. Request
fields cannot override the prompt, setup files, assertions, or commands; `AgentCommand` and
`CommandExitZero` are always absent. API results omit model output and raw internal errors. No
trace content is used by the evaluation.

## Consequences

### Positive

- An unscoped operator can inspect aggregate trace health without reading client conversation
  content or source paths.
- The built-in evaluation diagnoses a configured local model with bounded, synthetic input and
  cannot invoke a shell.
- Cloud/remote model execution, custom specs, and raw trace exports are excluded from the web API.

### Negative

- Full trace inspection/import and custom evaluation specs remain CLI-only.
- The smoke test evaluates only a simple response marker; it is not a replacement for M6 model,
  agentic, retrieval, or coding benchmarks.
- Evaluation is a real local model inference and must require an explicit user confirmation.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Return raw traces to the Agent-Control page | Trace records can contain cross-project/client prompts and tool results. |
| Accept arbitrary evaluation YAML or AgentCommand over HTTP | `evalrunner` can execute `/bin/sh -c`; a management token must not become a command runner. |
| Use cloud evaluation or user prompts | Could send private source/client content outside the Brain host. |
| Persist full evaluation output | Would create another store for generated/user data and error text. |

## Follow-up

- [ ] If raw trace detail is needed in a UI, add a project-bound access token and explicit
      content-redaction contract; do not reuse the unscoped settings page.
- [ ] Keep full eval-runner custom task execution local CLI-only unless a separately authorized,
      schema-limited suite design is adopted.
