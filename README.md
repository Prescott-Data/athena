# Athena MemOS — Memory Operating System

Athena MemOS is a production-grade, multi-tenant **memory service** for AI agents. It provides human-like recollection by implementing a three-tier memory architecture — Short-Term (STM), Mid-Term (MTM), and Long-Term (LTM) — backed by Redis, MongoDB, Milvus, and ArangoDB. It exposes a dual gRPC + REST HTTP API and is deployed on Azure (Container Apps for dev, AKS for staging/production).

> For the conceptual design and physics-based "Orbital Mechanics" principles see [docs/ARCHITECTURE_EVOLUTION.md](docs/ARCHITECTURE_EVOLUTION.md).  
> For ArangoDB production infra requirements see [docs/ARANGODB_INFRA_SPEC.md](docs/ARANGODB_INFRA_SPEC.md).  
> For cross-service (Dromos) integration design see [docs/ATHENA_UNIFIED_ARCHITECTURE.md](docs/ATHENA_UNIFIED_ARCHITECTURE.md).  
> For current tech debt see [techdebt.md](./techdebt.md).

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Memory Tiers](#2-memory-tiers)
3. [Data Flow — End to End](#3-data-flow--end-to-end)
4. [Background Schedulers](#4-background-schedulers)
5. [API Reference](#5-api-reference)
6. [Authentication & Multi-Tenancy](#6-authentication--multi-tenancy)
7. [Configuration Reference](#7-configuration-reference)
8. [Database Schemas](#8-database-schemas)
9. [Prometheus Metrics](#9-prometheus-metrics)
10. [CI/CD Pipeline](#10-cicd-pipeline)
11. [Local Development](#11-local-development)
12. [Go Client SDK](#12-go-client-sdk)
13. [Deployment](#13-deployment)

---

## 1. Architecture Overview

```
┌───────────────────────────────────────────────────────────────────────┐
│                             Clients                                   │
│        gRPC native  ·  REST/HTTP (grpc-gateway)  ·  Go SDK            │
└───────────────────────────────┬───────────────────────────────────────┘
                                │
┌───────────────────────────────▼───────────────────────────────────────┐
│                    API Server  (port 8080 / 9090)                     │
│   Auth (API-Key · JWT · mTLS)  ·  Rate Limiting  ·  Prometheus /metrics │
└────┬──────────────────┬────────────────────┬──────────────────────────┘
     │                  │                    │
┌────▼──────┐  ┌────────▼────────┐  ┌───────▼──────────────────────────┐
│ STM Layer │  │   MTM Layer     │  │           LTM Layer               │
│  Redis    │  │ MongoDB + Milvus│  │    ArangoDB (athena_ltm)          │
│ (hot path)│  │ (cognitive      │  │    Graph: Identities, Concepts,   │
│           │  │  chains)        │  │    Tools, Projects + MemoryEdges  │
└────┬──────┘  └────────┬────────┘  └───────┬──────────────────────────┘
     │                  │                    │
     │   Background Workers / Schedulers     │
     │  ┌───────────────┐  ┌──────────────┐  │
     └──│    Worker     │  │   Promoter   │──┘
        │ (event-driven)│  │  (30m ticker)│
        └───────────────┘  └──────┬───────┘
                                  │  ┌────────────────┐
                                  └──│    Archiver    │
                                     │  (60m ticker)  │
                                     └────────────────┘
                ┌────────────────────────────────┐
                │  K8s CronJob (03:00 UTC daily) │
                │  POST /api/v1/admin/analytics/ │
                │  trigger → Pregel community    │
                │  detection + bridge entities   │
                └────────────────────────────────┘
```

### Component Map

| Package | Path | Role |
|---|---|---|
| gRPC service | `internal/server/server.go` | Request handling, dual-write orchestration |
| STM cache | `internal/memory/stm_cache.go` | Redis sliding window + event coalescing |
| STM store | `internal/memory/stm_store.go` | Core orchestration: embeddings, LLM calls, MTM formation |
| Worker | `internal/memory/worker.go` | Background chain-break detector (BRPop loop) |
| Task queue | `internal/memory/task_queue.go` | Redis-backed scoped queue dispatch |
| Promoter | `internal/memory/promoter.go` | Heat scoring → LTM promotion scheduler |
| Heat scorer | `internal/memory/mtm_heat_scoring.go` | Ebbinghaus decay model |
| Session manager | `internal/memory/mtm_session_manager.go` | Chain merge / deduplication |
| Topic analyzer | `internal/memory/mtm_topic_analyzer.go` | LLM-powered topic extraction |
| Quality validator | `internal/memory/mtm_quality_validator.go` | Chain coherence gating |
| Continuity analyzer | `internal/memory/mtm_continuity_analyzer.go` | LLM gray-zone arbitration |
| Parallel processor | `internal/memory/mtm_parallel_processor.go` | Concurrent MTM pipeline steps |
| Graph extractor | `internal/memory/graph_extractor.go` | LLM → entity-relation triple extraction |
| LTM writer | `internal/memory/ltm_writer.go` | ArangoDB AQL UPSERT for graph nodes/edges |
| LTM reader | `internal/memory/ltm_reader.go` | ArangoDB graph traversal for context retrieval |
| Analytics | `pkg/memory/analytics.go` | Pregel Label Propagation + bridge entity detection |
| Milvus client | `internal/memory/milvus_client.go` | Vector store for chain embeddings |
| Blob storage | `internal/storage/` | Claim Check pattern for binary payloads |
| Config | `internal/config/config.go` | All env var defaults and parsing |
| Auth middleware | `api/middleware/` | JWT / API-Key / mTLS enforcement |
| Go SDK | `pkg/memoryos/` | Client library for consuming services |

---

## 2. Memory Tiers

### Short-Term Memory (STM) — Working Memory

**Storage:** Redis  
**Key pattern:** `stm:{tenantId}:{userId}:{agentId}`  
**TTL:** `STM_CACHE_TTL` hours (default: 3600 seconds)  

STM is the agent's immediate working memory. Every event is `LPUSH`'d into a Redis list (newest first) and trimmed to a sliding window of `STM_CACHE_MAX_TURNS` events. The list is also written to MongoDB (`cognitive_events`, `status="in_stm"`) for durability.

**Event coalescing:** Automation events sharing the same `workflow_id` or `execution_id` in `metadata` are deduplicated so repeated action-observation pairs don't pollute the context window.

**Automatic flush:** When the STM list grows to `STM_MAX_EVENTS_BEFORE_FLUSH` (default: 20), the older half is force-promoted to MTM even without a topic change.

---

### Mid-Term Memory (MTM) — Consolidated Experiences

**Storage:** MongoDB (`cognitive_chains`, `cognitive_events`) + Milvus (vector embeddings)

MTM contains semantically coherent **cognitive chains** — summaries of completed conversation topics extracted once the STM detects a topic shift. Each chain has:

- **Topic** — LLM-extracted main theme
- **Summary** — LLM-generated condensed narrative
- **Entities** — Named people, tools, projects referenced
- **Intrinsic importance** — 0.0–1.0 score returned by the LLM alongside the summary
- **Heat score** — Ebbinghaus decay value; chains accessed repeatedly stay warm longer
- **Recall strength** — Grows ×1.5 per access (with 12h cooldown to prevent cramming)
- **Vector embedding** — Summary embedded and stored in Milvus for semantic similarity search

**Chain merging:** New chains whose summaries are semantically similar (≥ `MEMORY_OS_MTM_MERGE_THRESHOLD`, default 0.85) to existing chains are merged rather than stored separately.

**Quality gating:** A `QualityValidator` computes a coherence + density score. Chains below the threshold for the configured `MEMORY_OS_MTM_QUALITY_MODE` are discarded and never stored.

---

### Long-Term Memory (LTM) — Knowledge Graph

**Storage:** ArangoDB — `athena_ltm` database

LTM is the permanent, structured representation of what the agent knows about the world and the user. It is a property graph:

**Vertex collections:**

| Collection | Contains |
|---|---|
| `Identities` | Users, agents, organizations |
| `Concepts` | Abstract ideas, topics |
| `Tools` | Software, languages, libraries, frameworks |
| `Projects` | Work items, repositories, initiatives |
| `Communities` | Cluster nodes (output of Pregel analytics) |

**Edge collection: `MemoryEdges`**

| Field | Type | Description |
|---|---|---|
| `_from`, `_to` | Document ref | Source and target vertices |
| `relation` | string | Must be one of: `USES`, `WORKS_ON`, `BUILT_FOR_CLIENT`, `STRUGGLES_WITH`, `EXHIBITS`, `EXPRESSED_INTEREST`, `RELATES_TO` |
| `context_nuance` | string | Free-text reason for the relationship |
| `confidence` | float64 | Simple average of all observed confidences: `(OLD + NEW) / 2` |
| `heat_score` | float64 | Heat at time of last update |
| `weight` | int | Number of times this edge has been reinforced |
| `created_at`, `last_seen` | datetime | Lifecycle timestamps |

**Edge interception:** The LTM writer validates all LLM-generated relation labels. Unrecognised verbs are automatically corrected to `RELATES_TO` (tracked by `memos_ltm_edge_interceptions_total` metric).

**Community detection (Pregel):** A Kubernetes CronJob hits `POST /api/v1/admin/analytics/trigger` at 03:00 UTC daily. This runs a Pregel Label Propagation algorithm over the entire `athena_ltm` graph to assign `community_id` values and then calculates bridge entity scores (nodes that span multiple communities).

> ⚠️ **Pregel must not run in-process.** The algorithm loads the full graph topology into memory and risks OOM on large tenants. It must always be triggered externally via the CronJob. See [docs/ARANGODB_INFRA_SPEC.md](docs/ARANGODB_INFRA_SPEC.md).

---

### Blob Storage — Claim Check Pattern

For events containing large binary payloads (file uploads, raw logs, JSON dumps), Athena uses the **Claim Check** pattern:

1. The server streams the binary payload to the configured blob store (MinIO/S3/GCS/Azure).
2. Only the resulting `BlobURI` (e.g., `s3://athena-blobs/events/abc123`) and `BlobMimeType` are stored in Redis and MongoDB.
3. During cold chain archival, the Archiver deletes orphaned blob objects to prevent unbounded storage costs.

---

## 3. Data Flow — End to End

### Phase 0 — Session Creation

```
POST /api/v1/sessions
  → MongoDB INSERT into cognitive_chains
      { tenantId, userId, agentId, chainId=UUID, status="active", topic="", summary="" }
  ← Returns session_id (= chainId)
```

### Phase 1 — STM Ingestion (Synchronous, Dual-Write)

```
POST /api/v1/sessions/{id}/interactions  (or  /events)
  ↓
server.go: StoreInteraction()
  ↓
  ┌── Redis LPUSH stm:{tenantId}:{userId}:{agentId}
  │     value = JSON(STMEvent{role, type, content, timestamp, metadata, chainId})
  │     TTL = STM_CACHE_TTL
  │
  └── MongoDB INSERT cognitive_events
        { ..., status="in_stm" }

  [Smart Trigger — only when role=user AND type=message]
  ↓
  Redis LPUSH memory_processing_queue:v1:{tenant}:{user}:{agent}
        value = TaskEnvelope{ type="cognitive_chain_check" }
  Redis LPUSH cognitive_work_queue
        value = "memory_processing_queue:v1:{tenant}:{user}:{agent}"
```

### Phase 2 — MTM Formation (Asynchronous, Worker)

```
Worker goroutine (×MEMORY_OS_NUM_WORKERS, default 2):
  BRPop(cognitive_work_queue)
  RPop(scoped queue)
  ↓
  1. Redis LRANGE stm:{...}
     - Normal: fetch 10 events
     - If totalCount >= STM_MAX_EVENTS_BEFORE_FLUSH (20): fetch ALL, force-promote older half

  2. Extract two most recent user messages

  3. Skip if events share workflow_id or execution_id (automation context)

  4. CreateEmbedding(newestMsg) + CreateEmbedding(prevMsg)
     → cosineSimilarity():
       ≥ CHAIN_SIM_HIGH (0.72)  → continue, no action
       < CHAIN_SIM_LOW  (0.52)  → CHAIN BREAK → proceed
       [low, high] gray zone    → LLM topic continuity check (yes/no, max 10 tokens)

  5. On CHAIN BREAK:
     a. acquireProcessingLock(chainId)  — idempotency via Redis
     b. LLM: CreateSegmentSummary → (summary, intrinsicImportance)
     c. LLM: TopicAnalyzer.AnalyzeTopics → main topic string
     d. LLM: ExtractEntities → []string of named entities
     e. QualityValidator.ValidateSegment → score + ShouldStore bool
        (if rejected → discard, log, return)
     f. MongoDB UPDATE cognitive_events SET status="in_mtm"
     g. SessionManager.ProcessNewChain:
        - Milvus similarity search for existing chains ≥ MEMORY_OS_MTM_MERGE_THRESHOLD (0.85)
        - MERGE or INSERT cognitive_chains in MongoDB
     h. CreateEmbedding(summary) → Milvus INSERT
     i. Redis LTRIM: keep only new-topic events in STM
```

### Phase 3 — LTM Promotion (Promoter, every 30 minutes)

```
Promoter.RunOnce(threshold=MEMORY_OS_PROMOTER_THRESHOLD, default 0.3):
  ↓
  For each active cognitive_chain:
    HeatScore = I_base × exp(-ΔT / (τ × S))
      I_base = 0.7 × intrinsicImportance + 0.3 × densityScore  (capped at 1.0)
      τ = HEAT_DECAY_TAU_HOURS (24h)
      S = recallStrength (starts 1.0, grows ×1.5 per recall, 12h cooldown)
      ΔT = hours since lastAccessedAt

    If heatScore >= threshold:
      LLM: GraphExtractor.ExtractGraphFromSummary → { nodes[], edges[] }
      LTMWriter.WriteExtractionToGraph():
        For each Node  → ArangoDB UPSERT into {Identities|Concepts|Tools|Projects}
        For each Edge  → ArangoDB UPSERT into MemoryEdges
                          confidence = (OLD.confidence + @new_confidence) / 2
                          weight = OLD.weight + 1
```

### Phase 4 — Cold Chain Archival (Archiver, every 60 minutes)

```
STMStore.ArchiveColdChains():
  MongoDB FIND cognitive_chains WHERE
    lastEventAt < now - 7 days  AND  heatScore < 0.1
  For each cold chain:
    Milvus DELETE vector embedding
    Blob Store DELETE orphaned blob objects
    MongoDB UPDATE status = "archived"
```

### Phase 5 — LTM Retrieval (Read Path)

```
GetContext(query?):
  1. Redis LRANGE stm:{...} → recent STM events
  2. If query: Milvus.SearchSimilarChains + MongoDB hydration
     Else: MongoDB FIND active chains ORDER BY lastEventAt DESC

SearchMemory(query):
  1. Milvus vector search on query embedding
  2. LTMReader.SearchByQuery:
     a. Tokenize query → keywords (≥4 chars, stop-words removed)
     b. ArangoDB: find vertex nodes matching keywords in Concepts,
        Identities, Projects, Tools
     c. ArangoDB GRAPH TRAVERSAL 1..2 hops OUTBOUND/INBOUND
        FILTER e.confidence >= 0.5
        SORT weight DESC, heat_score DESC
        LIMIT 50
  3. Merge and return MTM + LTM results
```

---

## 4. Background Schedulers

| Scheduler | Trigger mode | Interval | Env var | Action |
|---|---|---|---|---|
| **Worker** | Event-driven (Redis BRPop) | Immediate | `MEMORY_OS_NUM_WORKERS` (default `2`) | Chain-break detection, MTM formation |
| **Promoter** | Ticker | Every 30 min | `MEMORY_OS_PROMOTER_INTERVAL_MIN` | Heat scoring → LTM graph promotion |
| **Archiver** | Ticker | Every 60 min | `MEMORY_OS_ARCHIVER_INTERVAL_MIN` | Archive cold chains: delete Milvus vectors, mark MongoDB archived, GC blobs |
| **Analytics CronJob** | K8s CronJob (external) | Daily at 03:00 UTC | — | `POST /api/v1/admin/analytics/trigger` → Pregel Label Propagation + bridge entity scoring |

---

## 5. API Reference

All endpoints are available as both gRPC (port 9090) and REST HTTP (port 8080) via grpc-gateway. REST base path is `/api/v1`.

### Session Management

| Method | gRPC | REST | Description |
|---|---|---|---|
| Create session | `CreateSession` | `POST /sessions` | Creates a new session anchor in MongoDB. Returns `session_id`. |
| Get session | `GetSession` | `GET /sessions/{session_id}` | Returns session metadata. |
| Delete session | `DeleteSession` | `DELETE /sessions/{session_id}` | Deletes session record. |

### Memory Operations

| Method | gRPC | REST | Description |
|---|---|---|---|
| Store interaction | `StoreInteraction` | `POST /sessions/{session_id}/interactions` | Dual-writes a user↔agent message pair to Redis + MongoDB. Enqueues chain-check task on `role=user`. |
| Store event | `StoreEvent` | `POST /sessions/{session_id}/events` | Stores any typed event (`message`, `thought`, `action`, `observation`). Binary blobs are offloaded to blob storage (Claim Check). |
| Get context | `GetContext` | `GET /sessions/{session_id}/context` | Returns STM events + MTM chains. Pass `query` param for vector-augmented recall. |
| Search memory | `SearchMemory` | `POST /sessions/{session_id}/context/search` | Semantic vector search over MTM (Milvus) + LTM graph traversal (ArangoDB). |

### Analysis

| Method | gRPC | REST | Description |
|---|---|---|---|
| Analyze topics | `AnalyzeTopics` | `GET /sessions/{session_id}/analysis/topics` | LLM topic analysis of recent STM events. |
| Get heat metrics | `GetHeatMetrics` | `GET /sessions/{session_id}/analysis/heat` | Returns full Ebbinghaus heat score breakdown for a session. |
| Get segments | `GetSegments` | `GET /sessions/{session_id}/segments` | Returns paginated list of MTM cognitive chains for a session. |

### Admin

| Method | gRPC | REST | Description |
|---|---|---|---|
| Trigger analytics | `TriggerGraphAnalytics` | `POST /admin/analytics/trigger` | Manually triggers Pregel community detection + bridge entity calculation. Normally called by K8s CronJob only. |
| Health check | `HealthCheck` | `GET /health` | Returns service status and dependency connectivity map. |

### Observability (HTTP-only, no auth)

| Endpoint | Description |
|---|---|
| `GET /health` | Simple liveness probe (Gin, no auth) |
| `GET /metrics` | Prometheus metrics scrape endpoint |

---

## 6. Authentication & Multi-Tenancy

### Tenant Hierarchy

```
tenant_id
  └── user_id
        └── agent_id
```

`tenant_id` and `user_id` are always resolved from the authentication token or API key configuration — never trusted from the request body. All MongoDB indexes, Redis key patterns, Milvus field filters, and ArangoDB node properties scope data by this hierarchy.

### Auth Modes

All three modes are disabled by default and can be independently enabled:

| Mode | Env flag | Header |
|---|---|---|
| API Key | `MEMORY_OS_REQUIRE_API_KEY=true` | `X-API-Key: <key>` |
| JWT | `MEMORY_OS_REQUIRE_JWT=true` | `X-JWT-Token: <token>` |
| mTLS | `MEMORY_OS_REQUIRE_MTLS=true` | Client certificate (TLS handshake) |

**Required JWT claims:** `tenant_id`, `user_id`

---

## 7. Configuration Reference

### Server

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_PORT` | `8080` | HTTP / REST port |
| `MEMORY_OS_GRPC_PORT` | `9090` | gRPC port |
| `MEMORY_OS_READ_TIMEOUT` | `30` | HTTP read timeout (seconds) |
| `MEMORY_OS_WRITE_TIMEOUT` | `30` | HTTP write timeout (seconds) |
| `MEMORY_OS_NUM_WORKERS` | `2` | Background worker goroutines |
| `MEMORY_OS_ENABLE_TLS` | `false` | Enable TLS on HTTP server |
| `MEMORY_OS_TLS_CERT_FILE` | `""` | Path to TLS certificate |
| `MEMORY_OS_TLS_KEY_FILE` | `""` | Path to TLS private key |

### Authentication

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_REQUIRE_API_KEY` | `false` | Enforce `X-API-Key` header |
| `MEMORY_OS_REQUIRE_JWT` | `false` | Enforce `X-JWT-Token` header |
| `MEMORY_OS_REQUIRE_MTLS` | `false` | Enforce mutual TLS |
| `MEMORY_OS_API_KEY` | `default-api-key` | Valid API key value |
| `MEMORY_OS_JWT_SECRET` | `your-secret-key` | JWT HMAC signing secret |
| `MEMORY_OS_CLIENT_CA_CERT_FILE` | `""` | Path to CA certificate for mTLS |

### Redis (STM + Task Queues)

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_REDIS_HOST` | `localhost` | Redis hostname |
| `MEMORY_OS_REDIS_PORT` | `6379` | Redis port |
| `MEMORY_OS_REDIS_PASSWORD` | `""` | Redis password |
| `MEMORY_OS_REDIS_DB` | `0` | Redis database index |
| `REDIS_POOL_SIZE` | `10` | Connection pool size |
| `REDIS_POOL_TIMEOUT` | `5s` | Pool checkout timeout |
| `CACHE_TTL` | `3600` | Default TTL for STM entries (seconds) |
| `STM_CACHE_MAX_TURNS` | `10` | Max events in STM sliding window |
| `STM_CACHE_TTL` | `3600` | STM Redis key TTL (seconds) |
| `STM_TASK_TIMEOUT` | `300` | Worker task execution timeout (seconds) |

### MongoDB (MTM)

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_MONGODB_URI` | Required | Full MongoDB connection string |
| `MEMORY_OS_MONGODB_DATABASE` | `memory_os` | Database name |
| `MEMORY_OS_MONGODB_USERNAME` | `memory_user` | Auth username |
| `MEMORY_OS_MONGODB_PASSWORD` | Required | Auth password |

### Milvus (Vector Search)

| Variable | Default | Description |
|---|---|---|
| `MILVUS_HOST` | `localhost` | Milvus server hostname |
| `MILVUS_PORT` | `19530` | Milvus gRPC port |
| `MILVUS_DATABASE` | `""` | Milvus database name (optional) |
| `EMBEDDING_DIMENSIONS` | `1536` | Vector dimensionality — must match embedding model |

### ArangoDB (LTM Graph)

| Variable | Default | Description |
|---|---|---|
| `ARANGODB_URL` | `http://localhost:8529` | ArangoDB HTTP endpoint |
| `ARANGODB_USER` | `root` | ArangoDB username |
| `ARANGODB_PASSWORD` | Required | ArangoDB password |
| `ARANGODB_DATABASE` | `athena_ltm` | Target database name |

### LLM & Embeddings

| Variable | Default | Description |
|---|---|---|
| `LLM_PROVIDER` | `azure` | LLM backend: `azure` or `gemini` |
| `LLM_BASE_URL` | `""` | LLM API base URL |
| `LLM_MODEL_NAME` | `gpt-4` | Chat completion model name |
| `LLM_API_VERSION` | `2023-05-15` | Azure API version (if `azure`) |
| `LLM_TIMEOUT_SECONDS` | `10` | Default LLM call timeout |
| `LLM_EMBEDDING_TIMEOUT_SEC` | `15` | Embedding call timeout |
| `LLM_SUMMARY_TIMEOUT_SEC` | `20` | Summary generation timeout |
| `LLM_RATE_LIMIT_PER_MINUTE` | `50` | Max LLM calls per user per minute |
| `LLM_CIRCUIT_BREAKER_THRESHOLD` | `5` | Consecutive failures to open circuit |
| `LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS` | `60` | Circuit breaker reset window |
| `AZURE_OPENAI_ENDPOINT` | `""` | Azure OpenAI resource endpoint |
| `AZURE_OPENAI_API_KEY` | `""` | Azure OpenAI API key |
| `GEMINI_API_KEY` | `""` | Google Gemini API key |
| `EMBEDDING_BASE_URL` | `""` | Embedding model endpoint |
| `EMBEDDING_MODEL_NAME` | `text-embedding-ada-002` | Embedding model name |
| `EMBEDDING_API_VERSION` | `2023-05-15` | Embedding API version |

### Blob Storage

| Variable | Default | Description |
|---|---|---|
| `BLOB_PROVIDER` | `""` (disabled) | `minio`, `s3`, `gcs`, or `azure` |
| `BLOB_ENDPOINT` | `localhost:9000` | Blob store endpoint |
| `BLOB_BUCKET` | `athena-blobs` | Bucket/container name |
| `BLOB_ACCESS_KEY` | Required | Access key / client ID |
| `BLOB_SECRET_KEY` | Required | Secret key / client secret |
| `BLOB_USE_SSL` | `false` | Enable TLS for blob store |
| `BLOB_REGION` | `us-east-1` | Storage region |

### STM Thresholds (Chain-Break Detection)

| Variable | Default | Description |
|---|---|---|
| `CHAIN_SIM_HIGH` | `0.72` | Cosine similarity above this → topic continues |
| `CHAIN_SIM_LOW` | `0.52` | Cosine similarity below this → topic shift, MTM flush |

> **Note:** Values in between invoke an LLM arbitration call. Narrowing the gap reduces LLM spend but increases false positives/negatives.

### Heat Scoring (Ebbinghaus Model)

| Variable | Default | Description |
|---|---|---|
| `HEAT_ALPHA` | `0.2` | [Reserved for composite scoring] |
| `HEAT_BETA` | `0.3` | [Reserved for composite scoring] |
| `HEAT_GAMMA` | `0.25` | [Reserved for composite scoring] |
| `HEAT_DELTA` | `0.15` | [Reserved for composite scoring] |
| `HEAT_EPSILON` | `0.1` | [Reserved for composite scoring] |
| `HEAT_WEIGHT_INTRINSIC` | `0.7` | Weight of LLM-assigned intrinsic importance in I_base |
| `HEAT_WEIGHT_DENSITY` | `0.3` | Weight of event density in I_base |
| `HEAT_DECAY_TAU_HOURS` | `24.0` | Forgetting time constant τ (hours) |
| `HEAT_RECALL_GROWTH` | `1.5` | Recall strength multiplier per access |
| `HEAT_COOLDOWN_HOURS` | `12.0` | Min hours between recall strength boosts (spaced repetition) |

### MTM Quality & Promoter / Archiver

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_MTM_QUALITY_MODE` | `balanced` | Quality gating strictness (strict, balanced, permissive) |
| `MEMORY_OS_MTM_HEAT_THRESHOLD` | `0.8` | Heat score threshold for high-priority promotion |
| `MEMORY_OS_MTM_MERGE_THRESHOLD` | `0.85` | Milvus cosine similarity threshold for chain merging |
| `MEMORY_OS_MTM_MAX_SEGMENTS` | `50` | Max MTM chains to return in GetContext |
| `MEMORY_OS_PROMOTER_INTERVAL_MIN` | `30` | Promoter ticker interval (minutes) |
| `MEMORY_OS_PROMOTER_THRESHOLD` | `0.3` | Minimum heat score to trigger LTM promotion |
| `MEMORY_OS_ARCHIVER_INTERVAL_MIN` | `60` | Archiver ticker interval (minutes) |
| `MTM_ARCHIVE_SCAN_DAYS` | `7` | Age threshold for cold chain candidates |
| `MTM_FREEZING_POINT` | `0.1` | Heat score below which chains are archived |

### LTM behaviour

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_LTM_ENABLED` | `true` | Enable LTM graph writing (disable to use MTM only) |
| `MEMORY_OS_LTM_PERSONA_THRESHOLD` | `0.7` | Confidence threshold for persona node creation |
| `MEMORY_OS_LTM_KG_ENABLED` | `true` | Enable knowledge graph extraction by LLM |

---

## 8. Database Schemas

### MongoDB — `cognitive_events`

```
{
  _id:            ObjectID
  tenantId:       string
  userId:         string
  agentId:        string
  chainId:        string          // FK → cognitive_chains.chainId
  eventIndex:     int
  role:           "user" | "agent" | "system"
  type:           "message" | "thought" | "action" | "observation"
  content:        string
  blobUri:        string?         // set when payload was offloaded to blob store
  blobMimeType:   string?
  status:         "in_stm" → "in_mtm" → "archived"
  metadata:       map[string]any  // workflow_id, execution_id, project_id, etc.
  createdAt:      time.Time
}
Indexes: compound(tenantId, userId, agentId)
```

### MongoDB — `cognitive_chains`

```
{
  _id:                  ObjectID
  tenantId, userId, agentId: string
  chainId:              string          // = session_id for session anchor records
  topic:                string          // LLM-extracted topic theme
  summary:              string          // LLM-generated chain summary
  entities:             []string        // named entities
  startedAt:            time.Time
  lastEventAt:          time.Time
  lastAccessedAt:       time.Time?      // for spaced repetition cooldown
  eventCount:           int
  status:               "active" | "archived"
  archivedAt:           time.Time?
  intrinsicImportance:  float64         // 0.0–1.0, from LLM
  recallStrength:       float64         // starts 1.0, grows ×1.5 per recall
  densityScore:         float64         // event-type and metadata richness
  heatScore:            float64         // I_base × exp(-ΔT / (τ × S))
  heatFactors:          HeatFactors     // breakdown struct
  metadata:             map[string]string
}
Indexes: compound(tenantId, userId, agentId), lastEventAt DESC
```

### Redis Key Patterns

| Pattern | Type | Contents | TTL |
|---|---|---|---|
| `stm:{tenantId}:{userId}:{agentId}` | List | JSON `STMEvent` objects (LPUSH, newest first) | `STM_CACHE_TTL` |
| `cognitive_work_queue` | List | Scoped queue name strings | None |
| `memory_processing_queue:v1:{tenant}:{user}:{agent}` | List | JSON `TaskEnvelope` objects | None |
| `task_results:v1:{tenant}:{user}:{agent}:{taskId}` | String | JSON task result | Short |
| `processing_lock:{chainId}` | String | Idempotency lock | Short |
| `llm_rate_limit:{userId}` | String | Request counter | 1 minute |
| `embedding_cache:{sha256(text+model)}` | String | JSON `EmbeddingData` | Configurable |

### Milvus Collections

| Collection | Fields |
|---|---|
| `stm_embeddings` | `id`, `tenantId`, `userId`, `agentId`, `chainId`, `embedding` (float vector, `EMBEDDING_DIMENSIONS` dims) |
| `segment_embeddings` | Same schema — for MTM chain summary embeddings |

### ArangoDB — `athena_ltm`

**Vertex collections:** `Identities`, `Concepts`, `Tools`, `Projects`, `Communities`

All vertices carry: `_key`, `name`, `tenantId`, `created_at`, `last_seen`  
Community-annotated vertices also carry: `community_id`, `is_bridge` (bool), `bridge_score` (float)

**Edge collection: `MemoryEdges`**

```
{ _from, _to, relation, context_nuance, confidence, heat_score, weight, created_at, last_seen }
```

Valid `relation` values: `USES`, `WORKS_ON`, `BUILT_FOR_CLIENT`, `STRUGGLES_WITH`, `EXHIBITS`, `EXPRESSED_INTEREST`, `RELATES_TO`

**FetchContext AQL filter:** `FILTER e.confidence >= 0.5` — edges below this threshold are invisible to all retrieval queries.

---

## 9. Prometheus Metrics

Exposed at `GET /metrics`.

| Metric | Type | Description |
|---|---|---|
| `stm_cache_ops_total` | Counter | STM get/set operations; labels: `op`, `result` |
| `embedding_latency_seconds` | Histogram | Azure OpenAI embedding latency |
| `milvus_op_latency_seconds` | Histogram | Milvus operation latency |
| `cosine_similarity_distribution` | Histogram | Distribution of cosine scores (buckets 0–1) |
| `cosine_gate_decisions_total` | Counter | Chain-break gate outcomes; labels: `decision`, `result` |
| `llm_fallback_calls_total` | Counter | Rate-limited or circuit-broken LLM calls; labels: `reason`, `result` |
| `dialogue_chain_decision_latency_seconds` | Histogram | LLM gray-zone arbitration latency |
| `memos_promoter_chains_evaluated_total` | Counter | MTM chains evaluated by promoter per run |
| `memos_promoter_chains_promoted_total` | Counter | MTM chains promoted to LTM per run |
| `memos_heat_score_distribution` | Histogram | Distribution of computed heat scores |
| `memos_extractor_llm_duration_seconds` | Histogram | Graph extraction LLM call latency |
| `memos_extractor_schema_failures_total` | Counter | LLM returned invalid JSON for graph extraction |
| `memos_ltm_arango_upsert_duration_seconds` | Histogram | ArangoDB AQL upsert latency |
| `memos_ltm_nodes_written_total` | Counter | Nodes upserted to ArangoDB |
| `memos_ltm_edges_written_total` | Counter | Edges upserted to ArangoDB |
| `memos_ltm_edge_interceptions_total` | Counter | Rogue LLM relation verbs corrected to RELATES_TO |
| `memos_ltm_fetch_duration_seconds` | Histogram | ArangoDB graph traversal latency |
| `memos_ltm_nodes_read_total` | Counter | Nodes returned by LTM read path |
| `memos_ltm_edges_read_total` | Counter | Edges returned by LTM read path |
| `memos_ltm_fetch_errors_total` | Counter | ArangoDB traversal failures |
| `memos_ltm_fetch_no_results_total` | Counter | Traversals returning zero nodes |
| `memos_analytics_pregel_duration_seconds` | Histogram | Pregel community detection wall time |
| `memos_analytics_bridge_calc_duration_seconds` | Histogram | Bridge entity scoring job duration |
| `memos_analytics_bridges_found_total` | Gauge | Bridge nodes discovered in last analytics run |
| `BlobStorageOps` | Counter | Blob upload/download operations; labels: `op`, `provider`, `result` |
| `BlobPayloadBytes` | Histogram | Payload sizes transferred to/from blob store; label: `provider` |

---

## 10. CI/CD Pipeline

All three branch pipelines (`dev`, `staging`, `main`) follow the same structure. `dev` deploys to Azure Container Apps; `staging` and `main` deploy to AKS.

```
1. Generate Protobuf
   apt-get install protobuf-compiler
   go install protoc-gen-go + protoc-gen-go-grpc + protoc-gen-grpc-gateway
   ./scripts/generate.sh  →  api/grpc/gen/*.go  (passed as Bitbucket artifacts)

2. Parallel:
   ├── Lint:  go fmt ./...  +  go vet ./...  +  go mod tidy
   └── Test:  go test ./pkg/memoryos ./api/middleware  (unit only; integration skipped in CI)

3. Build Binary:  CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w"  →  artifact: memory-server

4. Docker Build & Push:  docker build → push to Azure Container Registry (dromos ACR)

5. Deploy:
   dev     → az containerapp update ... (Azure Container App: memory-os)
   staging → kubectl set image deployment/memory-os ... (AKS: dromos-console-aks-staging)
   main    → kubectl set image deployment/memory-os ... (AKS: dromos-console-aks-production)
```

**Generated protobufs are not committed to git.** They are generated fresh in every pipeline run and passed between steps as Bitbucket artifacts. If you need them locally, run `make generate`.

---

## 11. Local Development

### Prerequisites

- Go 1.24+
- Docker & Docker Compose
- `protoc` (for proto regeneration): `brew install protobuf`
- An LLM provider (Azure OpenAI **or** Google Gemini)

### Start Infrastructure

```bash
docker compose -f docker-compose.local.yml up -d
```

Starts:
- **Redis** — port 6379
- **MongoDB** — port 27017
- **Milvus** — port 19530
- **ArangoDB** — port 8529
- **MinIO** — ports 9000 (API) / 9001 (console)

### Initialize ArangoDB Collections

```bash
go run cmd/init-ltm/main.go
```

Creates the `athena_ltm` database and all vertex/edge collections with required indexes.

### Run the Server

```bash
# Copy and fill in credentials
cp .env.example .env.dev

# Start the server (loads .env.dev automatically)
go run cmd/memory-server/main.go
```

The server starts on:
- HTTP/REST: `http://localhost:8080`
- gRPC: `localhost:9090`
- Metrics: `http://localhost:8080/metrics`

### Run Tests

```bash
# All tests (requires running Docker infrastructure)
go test ./...

# Unit tests only (no infrastructure needed)
go test -short ./...

# Specific packages
go test -v ./internal/memory/... -timeout=5m
go test -v ./internal/database/... -timeout=30s

# With coverage
go test -cover ./...
```

### Regenerate Protobuf Stubs

If you modify `api/grpc/memory.proto`:

```bash
make generate
# or
./scripts/generate.sh
```

This generates:
- `api/grpc/gen/memory.pb.go` — protobuf message types
- `api/grpc/gen/memory_grpc.pb.go` — gRPC server/client interfaces
- `api/grpc/gen/memory.pb.gw.go` — grpc-gateway REST transcoder
- `docs/api/openapi.json` — OpenAPI v2 specification

**Do not commit the generated `*.pb.go` files.** They are in `.gitignore` and generated fresh by CI.

### Makefile Targets

```bash
make build        # Build memory-server binary
make run          # Run the server (requires .env)
make test         # go test ./...
make test-short   # go test -short ./...
make test-e2e     # Load .env.dev and run all tests
make lint         # go fmt + go vet + golangci-lint + go mod tidy
make generate     # Regenerate protobuf stubs and OpenAPI docs
make clean        # Remove build artifacts
make ci           # lint + test + build (local CI simulation)
```

### Run the E2E Simulation

```bash
go run cmd/simulate/main.go
```

Runs 13 end-to-end scenarios: session creation, interactions, STM/MTM recall, blob storage stress testing, and chain archival.

### Verify ArangoDB Graph

```bash
go run cmd/verify/main.go
go run cmd/verify_analytics/main.go
```

---

## 12. Go Client SDK

```go
import "bitbucket.org/dromos/memory-os/pkg/memoryos"

client := memoryos.NewClient(memoryos.ClientConfig{
    BaseURL:  "https://memory-os.yourdomain.com",
    APIKey:   "your-api-key",
    JWTToken: "your-jwt-token", // or just APIKey
    Timeout:  30 * time.Second,
})

// Create session
session, err := client.CreateSession(ctx, &memoryos.CreateSessionRequest{
    UserID:  "user123",
    AgentID: "agent456",
})

// Store a turn
_, err = client.StoreInteraction(ctx, session.SessionID, &memoryos.StoreInteractionRequest{
    UserMessage:   "What is the Ebbinghaus forgetting curve?",
    AgentResponse: "It's a mathematical formula describing memory decay over time...",
    Timestamp:     time.Now(),
})

// Get context (most recent STM + relevant MTM)
context, err := client.GetContext(ctx, session.SessionID, 10)
for _, turn := range context.RecentTurns {
    fmt.Println(turn.Content)
}

// Semantic search across all memory tiers
results, err := client.SearchMemory(ctx, session.SessionID, &memoryos.SearchRequest{
    Query:               "machine learning frameworks",
    Limit:               5,
    SimilarityThreshold: 0.7,
})
```

---

## 13. Deployment

### Docker

The production Dockerfile uses a multi-stage build:

```dockerfile
# Stage 1: Build
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o memory-server cmd/memory-server/main.go

# Stage 2: Run
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/memory-server .
EXPOSE 8080 9090
CMD ["./memory-server"]
```

### Kubernetes (AKS)

Required environment variables for AKS pods are set via `kubectl` during the pipeline deploy step. All sensitive values (credentials, API keys) must be in Kubernetes Secrets, not ConfigMaps.

**Minimum resource allocation (production):**

| Component | CPU request | CPU limit | Memory request | Memory limit |
|---|---|---|---|---|
| memory-os pod | 500m | 2000m | 512Mi | 2Gi |
| ArangoDB DBServer | 4 vCPU | 8 vCPU | 16Gi | 32Gi |
| Redis | 250m | 500m | 256Mi | 512Mi |
| Milvus | 1000m | 2000m | 2Gi | 4Gi |

For ArangoDB storage and IOPS requirements, see [docs/ARANGODB_INFRA_SPEC.md](docs/ARANGODB_INFRA_SPEC.md).

### Required Kubernetes CronJob

Community detection **must** be scheduled externally. Apply this manifest to the `dromos-memory-os` namespace:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: memory-os-analytics
  namespace: dromos-memory-os
spec:
  schedule: "0 3 * * *"  # 03:00 UTC daily
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: trigger
            image: curlimages/curl:latest
            command:
            - curl
            - -X
            - POST
            - -H
            - "X-API-Key: $(MEMORY_OS_API_KEY)"
            - http://memory-os.dromos-memory-os.svc.cluster.local:8080/api/v1/admin/analytics/trigger
          restartPolicy: OnFailure
```

---

## API Documentation

The full OpenAPI v2 specification is at `docs/api/openapi.json`. To view interactively:

1. Open [Swagger Editor](https://editor.swagger.io/)
2. File → Import `docs/api/openapi.json`

Or serve locally:
```bash
docker run -p 8081:8080 -e SWAGGER_JSON=/api/openapi.json \
  -v $(pwd)/docs/api:/api swaggerapi/swagger-ui
```

---

*Athena MemOS — giving AI the gift of memory.*
