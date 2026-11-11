# ADR-0018: Brain-owned configured-root RAG management

- **Date:** 2026-11-10
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0016 (adds a narrow local-knowledge management capability)

## Context

Brain already indexes local directories and serves hybrid search over a private SQLite store.
The CLI currently accepts an arbitrary directory path on each indexing invocation. Exposing
that argument over HTTP would let a web UI turn a management credential into a host filesystem
reader. Indexing also sends chunks to the configured embedding adapter, so remote/cloud egress
must be excluded from this operator surface.

## Decision

Knowledge roots are registered by an explicit local Brain CLI owner action and stored in the
owner-only Brain configuration as canonical absolute paths, collection IDs, names, and embedding
model IDs. Registration refuses filesystem root and any path that contains or is inside Brain's
private home. Agent-Control HTTP accepts only a registered source ID; it never accepts or returns
a filesystem path, collection selector, embedding model, or raw file content from an index job.

The API may list aggregate source/collection metadata, reindex a registered source as one bounded
cancellable job, and search only its associated collection. Search results are delivered only to
the same-origin Brain UI. Indexing/search are restricted to an already-installed catalog
embedding model on the configured loopback Ollama adapter. No cloud/remote embeddings, chat
generation, file upload, arbitrary collection/database access, or source-root creation is
exposed.

`WalkFiles` skips symlink entries, and every indexed file read verifies the resolved path stays
under the canonical root and matches the opened file identity. Cancelled indexing may leave
per-file partial commits; the job reports cancellation and a retry is incremental.

## Consequences

### Positive

- The browser can manage explicitly registered RAG sources without exposing host filesystem paths.
- File symlinks cannot be used to index data outside a registered root.
- Embeddings remain on the configured local Ollama endpoint and query/chunk content is not logged.

### Negative

- Source registration/removal is CLI-only; the management UI can index/search existing sources.
- Index job state is in-memory; restart cancels a running job, leaving partial indexed data that a
  retry can converge.
- Search results can contain indexed content, so only the Brain-origin same-origin console may
  request and render them.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Accept a filesystem path in HTTP | Allows arbitrary host file reads through the management credential. |
| Accept a collection name from the browser | Could expose unregistered collections and widen the data boundary. |
| Use any configured backend for embeddings | May send local document chunks to an external/cloud provider. |
| Make indexing one transaction/job | The current indexer commits incrementally; preserving bounded retryable behavior is safer than replacing it in this slice. |

## Follow-up

- [ ] If UI source add/remove is needed, add a safe root-picker/allowlist design; do not add a free-form HTTP path.
- [ ] Keep RAG search same-origin and redact query/results from logs and telemetry.
- [ ] Add skills/MCP, trace/evaluation, and forge management only under separate data-boundary ADRs.
