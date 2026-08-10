# API Reference

The complete `MemoryService` surface. Every operation is defined once in `api/grpc/memory.proto` and served as gRPC (port 9090) and REST (port 8080); the REST mappings below are generated from the proto's HTTP annotations. Conventions (headers, error mapping, auth) are in [Using the REST API](../guides/rest-api.md).

**Implementation status legend:** <span class="nx-badge nx-badge-primary">Implemented</span> <span class="nx-badge nx-badge-beta">Planned</span>

| Operation | REST | Status |
|---|---|---|
| [CreateSession](#createsession) | `POST /api/v1/sessions` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetSession](#getsession) | `GET /api/v1/sessions/{session_id}` | <span class="nx-badge nx-badge-beta">Planned</span> |
| [DeleteSession](#deletesession) | `DELETE /api/v1/sessions/{session_id}` | <span class="nx-badge nx-badge-beta">Planned</span> |
| [StoreInteraction](#storeinteraction) | `POST /api/v1/sessions/{session_id}/interactions` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [StoreEvent](#storeevent) | `POST /api/v1/sessions/{session_id}/events` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [GetContext](#getcontext) | `GET /api/v1/sessions/{session_id}/context` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [SearchMemory](#searchmemory) | `POST /api/v1/sessions/{session_id}/context/search` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [AnalyzeTopics](#analyzetopics) | `GET /api/v1/sessions/{session_id}/analysis/topics` | <span class="nx-badge nx-badge-beta">Planned</span> |
| [GetHeatMetrics](#getheatmetrics) | `GET /api/v1/sessions/{session_id}/analysis/heat` | <span class="nx-badge nx-badge-beta">Planned</span> |
| [GetSegments](#getsegments) | `GET /api/v1/sessions/{session_id}/segments` | <span class="nx-badge nx-badge-beta">Planned</span> |
| [HealthCheck](#healthcheck) | `GET /api/v1/health` | <span class="nx-badge nx-badge-primary">Implemented</span> |
| [TriggerGraphAnalytics](#triggergraphanalytics) | `POST /api/v1/admin/analytics/trigger` | <span class="nx-badge nx-badge-primary">Implemented</span> |

Planned operations are declared in the proto and routable, but their handlers return `Unimplemented`. They are documented here so clients can code against the contract.

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

`GET /api/v1/sessions/{session_id}` <span class="nx-badge nx-badge-beta">Planned</span>

Will return the `Session` object (`session_id`, `user_id`, `created_at`, `updated_at`, `metadata`). Currently returns `Unimplemented` (HTTP 501).

---

## DeleteSession

`DELETE /api/v1/sessions/{session_id}` <span class="nx-badge nx-badge-beta">Planned</span>

Will delete the session and cascade to its events. Currently returns `Unimplemented` (HTTP 501). Until then, sessions are cheap and abandoning one is harmless.

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

**Response** (`StoreEventResponse`): `success` (bool), `event_id` (string, reserved).

**Errors:** `NotFound` (session), `InvalidArgument` (bad `type`), `FailedPrecondition` (payload without blob store → HTTP 412), `Internal`.

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
| `similarity_threshold` | double | no | Accepted, **not enforced** <span class="nx-badge nx-badge-beta">Planned</span> |
| `filter` | map<string,string> | no | Metadata equality filter; all pairs must match |

**Response** (`SearchMemoryResponse`): `results` (SearchResult[]).

**SearchResult**

| Field | Type | Notes |
|---|---|---|
| `content` | string | Chain summary or enriched fact |
| `similarity_score` | double | Higher is closer |
| `source_type` | string | e.g. `dialogue_page` |
| `source_id` | string | Chain/entity ID |
| `timestamp` | timestamp | |

**Errors:** `NotFound` (session), `InvalidArgument` (empty query), `Internal`.

---

## AnalyzeTopics

`GET /api/v1/sessions/{session_id}/analysis/topics` <span class="nx-badge nx-badge-beta">Planned</span>

Will return `TopicSummary[]` (`topic`, `confidence`, `frequency`, `last_mentioned`). Currently `Unimplemented`. Today, topics are visible on chains via [`verifydb`](../guides/debug-tools.md) or in search results.

---

## GetHeatMetrics

`GET /api/v1/sessions/{session_id}/analysis/heat` <span class="nx-badge nx-badge-beta">Planned</span>

Will return `HeatMetrics` (`overall_heat`, `breakdown` as HeatFactors, `total_interactions`, `last_activity`). Currently `Unimplemented`. Heat is observable today through the `memos_heat_score_distribution` [metric](metrics.md) and `verifydb`.

---

## GetSegments

`GET /api/v1/sessions/{session_id}/segments` <span class="nx-badge nx-badge-beta">Planned</span>

Will return `Segment[]` (`content`, `topics`, `heat_factors`, `quality_score`, `created_at`). Currently `Unimplemented`.

---

## HealthCheck

`GET /api/v1/health` <span class="nx-badge nx-badge-primary">Implemented</span>

Dependency-aware health. Distinct from the auth-free Gin `/health` liveness route; this one runs through the normal API stack.

**Response** (`HealthCheckResponse`)

| Field | Type | Notes |
|---|---|---|
| `status` | string | `healthy` \| `degraded` |
| `dependencies` | map<string,string> | Per-store status (`redis`, `mongodb`), error text on failure |
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
| `GET /metrics` | never required | Prometheus text format ([Metrics reference](metrics.md)) |
