# Data Schemas

What Athena actually stores, store by store. Useful for debugging, capacity planning, and building read-only integrations. Treat these as observed layouts, not stable contracts: the API is the compatibility boundary, not the storage.

## Redis

| Key pattern | Type | TTL | Holds |
|---|---|---|---|
| `stm:{tenant}:{user}:{agent}` | list | 2h (sliding) | STM window, newest first (`LPUSH`); JSON-encoded events |
| `cognitive_work_queue` | list | none | Global dispatcher: names of scoped queues with pending work |
| `memory_processing_queue:v1:{tenant}:{user}:{agent}` | list | none | Task envelopes (`{id, type, payload, enqueued_at}`) per scope |
| `task_results:v1:{tenant}:{user}:{agent}:{taskId}` | string | 1h | `{success, message, processed_at}` |

The two-level queue design is explained in [The Cognitive Pipeline](../concepts/cognitive-pipeline.md#the-two-level-queue).

## MongoDB

Database: `MEMORY_OS_MONGODB_DATABASE` (local stack: `memory_os`).

### `cognitive_events`

The durable event log; one document per stored event.

| Field | Notes |
|---|---|
| `tenant_id`, `user_id`, `agent_id` | Scope |
| `session_id` | Originating session |
| `role`, `type`, `content` | Event body |
| `metadata` | String map (`workflow_id`, `execution_id`, `step_id`, `origin_service`, ...) |
| `blobUri`, `blobMimeType` | Set when the payload went to [blob storage](../guides/blob-storage.md) |
| `chainId` | Set once the event is claimed by a cognitive chain |
| `timestamp` | Event time |

### `cognitive_chains`

One document per [cognitive chain](../concepts/chain-formation.md).

| Field | Notes |
|---|---|
| `chainID` | Also the Milvus vector key |
| `tenant_id`, `user_id`, `agent_id` | Scope |
| `topic`, `summary`, `entities[]` | Distilled content |
| quality scores | From the validation gate |
| `heatScore`, `recallStrength` | Lifecycle state ([Heat & Decay](../concepts/heat-and-decay.md)) |
| `lastAccessedAt`, `lastEventAt`, `startedAt` | Decay inputs |
| `eventCount` | Size |
| `status` | `active` \| `archived` |
| `archivedAt` | Set by the archiver |

### Sessions

Session documents store `session_id`, scope, `metadata`, and timestamps; created by `CreateSession`.

## Milvus

| Collection | Vector per | Keyed by | Dimension |
|---|---|---|---|
| `segment_embeddings` | Cognitive chain | chain ID | `EMBEDDING_DIMENSIONS` (Azure 1536 / Gemini 768) |
| `stm_embeddings` | STM message (chain-break comparisons) | event | same |

Vectors are deleted when their chain is archived; the collection dimension is fixed at creation ([the trap](../guides/llm-providers.md#the-dimension-trap)).

## ArangoDB: the `athena_ltm` graph

Created by `init-ltm`. All documents carry tenant/user scope fields.

### Vertex collections

| Collection | Holds | Example `_key` |
|---|---|---|
| `Identities` | People, orgs, agents (anything with agency) | `john_doe` |
| `Tools` | Software, hardware, languages | `arangodb` |
| `Projects` | Repos, initiatives | `payments_service` |
| `Concepts` | Ideas, methodologies | `spaced_repetition` |
| `Communities` | Cluster metadata from [analytics](../concepts/graph-analytics.md) | |

Node fields: `name`, `created_at`, `last_seen`, plus `community_id`, `is_bridge`, `bridge_score` after analytics runs.

### Edge collection: `MemoryEdges`

| Field | Notes |
|---|---|
| `_from`, `_to` | Vertex handles |
| `relation` | One of `USES`, `WORKS_ON`, `BUILT_FOR_CLIENT`, `STRUGGLES_WITH`, `EXHIBITS`, `EXPRESSED_INTEREST`, `RELATES_TO` |
| `context_nuance` | Free-text qualifier; latest write wins |
| `confidence` | Running average of observations; retrieval filters `>= 0.5` |
| `weight` | Observation count; +1 per re-upsert; primary retrieval sort key |
| `heat_score` | Source chain's heat at promotion time |
| `created_at`, `last_seen` | |

Uniqueness is `(_from, _to, relation)`; re-observing a fact updates rather than duplicates ([UPSERT contract](../concepts/promotion-and-knowledge-graph.md#idempotent-writes-the-upsert-contract)). Composite indexes cover `(tenant, user, agent, relation)`.

## Blob storage

Objects are event payloads, stored under the configured bucket and referenced by `blobUri` from `cognitive_events`. Deleted by the archiver when the owning chain freezes; no other component writes here.

## Who writes what

| Store | Writers | Readers |
|---|---|---|
| Redis | API, workers | API, workers |
| MongoDB | API, workers, promoter, archiver | everything |
| Milvus | workers (insert), archiver (delete) | search, context-with-query |
| ArangoDB | promoter, analytics | search enrichment |
| Blob store | API (upload), archiver (delete) | consumers via URI |
