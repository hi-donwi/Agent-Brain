# ADR-0003: One pure-Go SQLite store with FTS5 and in-process vector search

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

lokal needs durable local state: indexed documents and chunks, lexical and dense
retrieval, agent traces, and the skill registry. It must run inside one static binary
(ADR-0001) and fit the smallest target, an 8 GB machine with a few GB of free disk.

The target corpus is personal: a code repository, a notes folder, a documentation set.
Ten thousand to one hundred thousand chunks per collection is the expected range.

M0 spike, `modernc.org/sqlite` v1.60.1 (SQLite 3.53.4) with `CGO_ENABLED=0` on an Apple
M3 (8 cores), synthetic data, median of 20 queries, top-10 by full scan:

| Chunks x dim | FTS5 bm25 p50 | Vector f32, 8 cores | Vector int8, 8 cores | int8 recall@10 | Cold load |
|---|---|---|---|---|---|
| 10k x 384 | 8 ms | 1.9 ms | 1.4 ms | 0.97 | 31 ms |
| 10k x 768 | 10 ms | 2.9 ms | 1.7 ms | 0.95 | 45 ms |
| 100k x 384 | 90 ms | 21 ms | 16 ms | 0.96 | 4.1 s |
| 100k x 768 | 91 ms | 27 ms | 18 ms | 0.95 | 3.4 s |

FTS5 numbers are the pessimistic case (60-word vocabulary, every row matches). Recall for
int8 was measured on random vectors, not real embeddings.

## Decision

We keep all lokal state in one SQLite database per lokal home, opened through
`modernc.org/sqlite` in WAL mode. Lexical search uses FTS5 (bm25). Dense search stores
L2-normalised float32 vectors as BLOBs and scans them in process as a dot product over a
resident slab, in parallel across cores. Hybrid results are fused with reciprocal rank
fusion. Each collection records its embedding model and dimension, and lokal refuses to
mix vectors from different models in one collection.

## Consequences

### Positive
- One file to back up, inspect, or delete; no server, no extension loading.
- Exact search: no ANN recall loss and no index build step.
- Under 30 ms dense search at 100k x 768 on the smallest target.

### Negative
- Cold load is 3-4 s at 100k chunks. `lokal serve` keeps the slab resident; one-shot
  CLI queries on large collections need a faster path (a memory-mapped flat vector
  sidecar is the leading option), decided in M3.
- Memory is 4 bytes x dim per chunk (300 MB at 100k x 768). int8 quantisation (4x less)
  is enabled only after it is measured on real embeddings with the M3 golden set.
- Beyond roughly 500k chunks per collection a full scan stops being interactive; that
  case needs a superseding ADR (pure-Go HNSW or IVF).

### Neutral
- Embeddings from Ollama are already L2-normalised (spike: norm 1.0000001); lokal
  normalises anyway so other backends behave the same.

## Alternatives considered

| Option | Why rejected |
|---|---|
| `sqlite-vec` extension | Needs cgo (ADR-0001) |
| Embedded vector DB (LanceDB, Chroma) | Native or Python dependencies; a second store beside SQLite |
| External vector server (Qdrant, pgvector) | A service to install and run, the opposite of easy setup |
| Pure-Go ANN index (HNSW) now | Unneeded at personal scale; adds recall loss and build cost. Revisit past ~500k chunks |

## Follow-up

- [ ] M3: schema and migrations; re-measure FTS5 and vectors on real text and embeddings
- [ ] M3: decide the cold-start path (mmap sidecar vs. resident only)
- [ ] M3: int8 enablement gated on golden-set recall
