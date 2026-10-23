# API Reference

The complete `MemoryService` surface. Every operation is defined once in `api/grpc/memory.proto` and served as gRPC (port 9090) and REST (port 8080); the REST mappings below are generated from the proto's HTTP annotations. Fields are listed with their proto names (`snake_case`); REST responses carry them in `lowerCamelCase` (`session_id` → `sessionId`), and requests accept either. Conventions (headers, error mapping, auth) are in [Using the REST API](../guides/rest-api.md).

**Implementation status legend:** <span class="nx-badge nx-badge-primary">Implemented</span> <span class="nx-badge nx-badge-beta">Planned</span>

| Operation | REST | Status |
|---|---|---|
| [CreateSession](#createsession) | `POST /api/v1/sessions` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetSession](#getsession) | `GET /api/v1/sessions/{session_id}` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [DeleteSession](#deletesession) | `DELETE /api/v1/sessions/{session_id}` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [EraseMemory](#erasememory) | `POST /api/v1/memory/erase` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [StoreInteraction](#storeinteraction) | `POST /api/v1/sessions/{session_id}/interactions` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [StoreEvent](#storeevent) | `POST /api/v1/sessions/{session_id}/events` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetContext](#getcontext) | `GET /api/v1/sessions/{session_id}/context` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [SearchMemory](#searchmemory) | `POST /api/v1/sessions/{session_id}/context/search` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [AnalyzeTopics](#analyzetopics) | `GET /api/v1/sessions/{session_id}/analysis/topics` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetHeatMetrics](#getheatmetrics) | `GET /api/v1/sessions/{session_id}/analysis/heat` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetSegments](#getsegments) | `GET /api/v1/sessions/{session_id}/segments` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [HealthCheck](#healthcheck) | `GET /api/v1/health` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [TriggerGraphAnalytics](#triggergraphanalytics) | `POST /api/v1/admin/analytics/trigger` | <span class="nx-badge nx-badge-primary">Implemented</span> |

---

## CreateSession

`POST /api/v1/sessions` · gRPC `memory.v1.MemoryService/CreateSession` <span class="nx-badge nx-badge-primary">Implemented</span>

Creates a session handle bound to a `(tenant, user, agent)` scope. Memory is keyed by the scope, not the session; see [Sessions & Identity](../concepts/sessions-and-identity.md).

**Request** (`CreateSessionRequest`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `tenant_id` | string | yes* | Ignored when JWT auth supplies it |
| `user_id` | string | yes* | Ignored when JWT auth supplies it |
| `agent_id` | string | no | Defaults to empty scope segment |
| `metadata` | map<string,string> | no | Stored on the session, returned by GetSession |

*With JWT auth enabled, identity comes from token claims and body values are ignored.

**Response** (`CreateSessionResponse`)

| Field | Type | Notes |
|---|---|---|
| `session_id` | string | Use in all subsequent paths |
| `created_at` | timestamp | RFC 3339 in JSON |

**Errors:** `InvalidArgument` (missing tenant/user), `Internal`.

---

## GetSession

`GET /api/v1/sessions/{session_id}` <span class="nx-badge nx-badge-primary">Implemented</span>

Returns the session's scope, metadata and activity. Use it to check that a cached `session_id` still exists before writing to it.

**Response** (`GetSessionResponse.session`)

| Field | Type | Notes |
|---|---|---|
| `session_id` | string | |
| `tenant_id`, `user_id`, `agent_id` | string | The scope the session writes to |
| `status` | string | `active` |
| `metadata` | map<string,string> | As given to `CreateSession` |
| `event_count` | int32 | Events stored through this session |
| `created_at` | timestamp | |
| `updated_at` | timestamp | Time of the latest stored event, or `created_at` if none |

**Errors:** `InvalidArgument` (empty ID), `NotFound`, `Internal`.

---

## DeleteSession

`DELETE /api/v1/sessions/{session_id}` <span class="nx-badge nx-badge-primary">Implemented</span>

Erases everything Athena holds for the session, for example to honour a deletion request:

- its entries in the scope's STM list (entries of other sessions in the same scope stay);
- every event stored through it, and their blob payloads;
- every MTM chain summarised from those events, with the chain and event vectors in Milvus. A chain formed from several sessions is deleted whole, because its summary contains this session's content; the other sessions keep their own events;
- the session record. Later calls with the ID return `NotFound`.

A chain formation already running for the session when it is deleted discards its result. Vectors and blobs are removed before anything in MongoDB, so a failed call leaves the session in place and can be retried.

Knowledge promoted to the LTM graph is removed when the graph records which chains each node and edge came from: an entry goes when no remaining chain supports it. Graph entries written before that provenance existed are not attributable and stay.

Data written before this release lacks the links between events, sessions and chains. Its chains are matched by time instead: a chain in the session's scope whose span covers one of the session's events is removed. A chain an older release merged from several chains can span another session's events too, and is then removed as well (its source events stay). Only a session ID resolves; passing a segment (chain) ID returns `NotFound`.

**Response** (`DeleteSessionResponse`)

| Field | Type | Notes |
|---|---|---|
| `success` | bool | |
| `stm_events_deleted` | int32 | |
| `events_deleted` | int32 | Includes copies made by releases before this fix |
| `chains_deleted` | int32 | |
| `vectors_deleted` | int32 | Chain and event vectors targeted |
| `blobs_deleted` | int32 | |
| `ltm_entries_deleted` | int32 | LTM graph nodes and edges no longer supported by any chain |

**Errors:** `InvalidArgument` (empty ID), `NotFound`, `Internal` (safe to retry).

---

## EraseMemory

`POST /api/v1/memory/erase` <span class="nx-badge nx-badge-primary">Implemented</span>

Erases everything Athena holds for a scope, for deletion requests and offboarding:

| Request | Erases |
|---|---|
| `{"tenant_id", "user_id"}` | One user: every session, STM list, event, blob, chain and vector of the user, across all agents, and the user's LTM graph |
| `{"tenant_id", "user_id", "agent_id"}` | One agent of a user: as above for that agent, including chains of the user's other agents that the agent's conversations were merged into (removed whole); from the graph, only what those chains contributed (the graph is shared by the user's agents) |
| `{"tenant_id"}` | A whole tenant |

While the erasure runs, the scope is closed: writes to its sessions and new sessions in it are refused with `FailedPrecondition` ("this memory is being erased"), and a final sweep after the session records are gone catches a write that was already in flight. Chain formations already running discard their result. Erased sessions stay closed; the scope reopens for new sessions when the erasure ends. Vectors, blobs and the graph are removed before MongoDB and session records go last, so a failed call can be retried; erasing an empty scope succeeds with zero counts.

**Response** (`EraseMemoryResponse`): `success`, and counts `sessions_deleted`, `stm_events_deleted`, `events_deleted`, `chains_deleted`, `vectors_deleted`, `blobs_deleted`, `ltm_entries_deleted`.

**Errors:** `InvalidArgument` (no `tenant_id`, or `agent_id` without `user_id`), `Internal` (safe to retry).

Writes (`CreateSession`, `StoreInteraction`, `StoreEvent`) to a scope being erased return `FailedPrecondition`; retry them after the erasure.

---

## StoreInteraction

`POST /api/v1/sessions/{session_id}/interactions` <span class="nx-badge nx-badge-primary">Implemented</span>

Records one user↔agent turn as two STM events and enqueues a cognitive-chain check. Usage patterns: [Storing Memory](../guides/storing-memory.md).

**Request** (`StoreInteractionRequest`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | path | |
| `user_message` | string | yes | Stored as `role: user, type: message` |
| `agent_response` | string | no | Stored as `role: agent, type: message` |
| `metadata` | map<string,string> | no | Attached to both events |
| `timestamp` | timestamp | no | Defaults to server time |

**Response** (`StoreInteractionResponse`)

| Field | Type | Notes |
|---|---|---|
| `success` | bool | |
| `interaction_id` | string | Reserved; currently always empty |

**Side effects:** dual-write to Redis + MongoDB; `cognitive_chain_check` task enqueued for the user message.

**Errors:** `NotFound` (session), `Internal`.

---

## StoreEvent

`POST /api/v1/sessions/{session_id}/events` <span class="nx-badge nx-badge-primary">Implemented</span>

Records a single event of any role/type, with optional binary payload.

**Request** (`StoreEventRequest`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | path | |
| `role` | string | no | `user` \| `agent` \| `system`; defaults to `system` |
| `type` | string | yes | `message` \| `thought` \| `action` \| `observation` |
| `content` | string | no | Text content |
| `metadata` | map<string,string> | no | `workflow_id`/`execution_id`/`step_id`/`origin_service` [change pipeline behavior](../guides/storing-memory.md#metadata-that-changes-behavior) |
| `timestamp` | timestamp | no | Defaults to server time |
| `payload` | bytes | no | Base64 in JSON; uploaded to [blob storage](../guides/blob-storage.md) |
| `mime_type` | string | with payload | e.g. `application/json` |

**Response** (`StoreEventResponse`): `success` (bool), `event_id` (string MongoDB
ObjectID identifying the durable `cognitive_events` record).

**Errors:** `NotFound` (session), `InvalidArgument` (bad `type`), `FailedPrecondition` (payload without blob store → HTTP 412), `Internal` (cache, blob, or durable event persistence failure).

---

## GetContext

`GET /api/v1/sessions/{session_id}/context` <span class="nx-badge nx-badge-primary">Implemented</span>

Returns the STM window plus MTM pages. Usage: [Retrieving Context](../guides/retrieving-context.md).

**Query parameters** (`GetContextRequest`)

| Parameter | Type | Default | Notes |
|---|---|---|---|
| `limit` | int32 | 10 | Max STM events |
| `query` | string | none | Switches MTM selection from recency to semantic vector search |
| `include_segments` | bool | false | Reserved <span class="nx-badge nx-badge-beta">Planned</span> |

**Response** (`GetContextResponse`)

| Field | Type | Notes |
|---|---|---|
| `stm_events` | STMEvent[] | Chronological; all four event types |
| `relevant_pages` | DialoguePage[] | MTM chains: semantic if `query` given, else recency |
| `segments` | Segment[] | Always empty <span class="nx-badge nx-badge-beta">Planned</span> |
| `user_persona` | UserPersona | Not populated <span class="nx-badge nx-badge-beta">Planned</span> |
| `ltpm` | LTPMContext | Always `{"status": "not_implemented"}` <span class="nx-badge nx-badge-beta">Planned</span>; use [SearchMemory](#searchmemory) for LTM |

**STMEvent** fields: `role`, `type`, `content`, `timestamp`, `metadata`, `blob_uri`, `blob_mime_type`.
**DialoguePage** fields: `id`, `session_id`, `topic`, `summary`, `timestamp`, `embedding` (not populated), `metadata`.

**Errors:** `NotFound`, `Internal`.

---

## SearchMemory

`POST /api/v1/sessions/{session_id}/context/search` <span class="nx-badge nx-badge-primary">Implemented</span>

Semantic search over the user's cognitive chains (all agents), enriched from the LTM graph. Usage: [Semantic Search](../guides/semantic-search.md).

**Request** (`SearchMemoryRequest`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | path | Scope resolves to the session's user |
| `query` | string | yes | Empty returns `InvalidArgument` |
| `limit` | int32 | no | Max results |
| `similarity_threshold` | double | no | Minimum cosine similarity (−1 to 1) for chain results; 0 or unset returns all |
| `filter` | map<string,string> | no | Metadata equality filter; all pairs must match |

**Response** (`SearchMemoryResponse`): `results` (SearchResult[]). Chain results come first, most similar first, followed by graph results (which have no score).

**SearchResult**

| Field | Type | Notes |
|---|---|---|
| `content` | string | Chain summary or enriched fact |
| `similarity_score` | double | Cosine similarity between the query and the chain summary (−1 to 1, higher is closer); 0 for graph results |
| `source_type` | string | e.g. `dialogue_page` |
| `source_id` | string | Chain/entity ID |
| `timestamp` | timestamp | |

**Errors:** `NotFound` (session), `InvalidArgument` (empty query), `Internal`.

---

## AnalyzeTopics

`GET /api/v1/sessions/{session_id}/analysis/topics` <span class="nx-badge nx-badge-primary">Implemented</span>

Runs the topic analyser (one LLM call) over the session's most recent 200 stored events.

**Response:** `topics`, a list of `TopicSummary`, strongest first.

| Field | Type | Notes |
|---|---|---|
| `topic` | string | |
| `confidence` | double | 0–1 |
| `frequency` | int32 | Events that mention the topic's keywords |
| `last_mentioned` | timestamp | Latest of those events |

**Errors:** `InvalidArgument`, `NotFound`, `Unavailable` (LLM call failed), `Internal`.

---

## GetHeatMetrics

`GET /api/v1/sessions/{session_id}/analysis/heat` <span class="nx-badge nx-badge-primary">Implemented</span>

Aggregates the current heat of the session's segments (the MTM chains summarised from its events). Heat follows the Ebbinghaus forgetting curve, `heat = base_importance × time_decay`, and decays with time, so it is computed at request time rather than read from the stored score.

**Response** (`GetHeatMetricsResponse.heat_metrics`)

| Field | Type | Notes |
|---|---|---|
| `overall_heat` | double | Mean heat of the session's segments |
| `max_heat` | double | Heat of the hottest segment |
| `breakdown` | HeatFactors | Mean `base_importance`, `time_decay` and `recall_strength` |
| `segment_count` | int32 | |
| `total_interactions` | int32 | Events stored through the session |
| `last_activity` | timestamp | Latest stored event |

`HeatFactors` fields 1–5 (`access_frequency` … `topic_importance`) are deprecated and always zero.

**Errors:** `InvalidArgument`, `NotFound`, `Internal`.

---

## GetSegments

`GET /api/v1/sessions/{session_id}/segments?limit=N` <span class="nx-badge nx-badge-primary">Implemented</span>

Returns the MTM chains summarised from the session's events, newest first. `limit` defaults to 10, maximum 100. A chain formed from several sessions appears for each of them.

**Response:** `segments`, a list of `Segment`.

| Field | Type | Notes |
|---|---|---|
| `id` | string | Chain ID |
| `session_id` | string | The requested session |
| `content` | string | Chain summary |
| `topics` | string[] | The chain's topic |
| `heat`, `heat_factors` | double, HeatFactors | Current heat and its factors |
| `quality_score` | double | Quality-gate score at formation (0 for chains formed before this field existed) |
| `event_count` | int32 | |
| `created_at`, `last_event_at` | timestamp | Times of the chain's first and last events |

**Errors:** `InvalidArgument`, `NotFound`, `Internal`.

---

## HealthCheck

`GET /api/v1/health` <span class="nx-badge nx-badge-primary">Implemented</span>

Dependency-aware health. Distinct from the auth-free Gin `/health` liveness route; this one runs through the normal API stack.

**Response** (`HealthCheckResponse`)

| Field | Type | Notes |
|---|---|---|
| `status` | string | `healthy` \| `degraded` |
| `dependencies` | map<string,string> | Per-dependency status (`redis`, `mongodb`, `milvus`, `llm`, `ltm`), error text on failure; any failure makes `status` `degraded` |
| `timestamp` | timestamp | |

---

## TriggerGraphAnalytics

`POST /api/v1/admin/analytics/trigger` <span class="nx-badge nx-badge-primary">Implemented</span>

Starts community detection + bridge scoring in the background; returns immediately. Operational guidance (scheduling, the never-in-process rule): [Running Graph Analytics](../guides/running-graph-analytics.md).

**Request:** empty body.
**Response:** `success` (bool), `message` (string).
**Errors:** `Internal` (promoter not initialized).

---

## Gin-native routes (outside the proto)

| Route | Auth | Purpose |
|---|---|---|
| `GET /health` | never required | Liveness: `{"status", "service": "memory-os", "timestamp"}`; `degraded` if Redis/MongoDB checks fail |
| `GET /metrics` | service key (tenant keys get `403`); none with `MEMORY_OS_METRICS_PUBLIC=true` or when authentication is off | Prometheus text format ([Metrics reference](metrics.md)) |
