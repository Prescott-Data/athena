# Configuration

Athena is configured entirely through environment variables; no config files are deployed per environment. This page covers the variables you must understand to run Athena at all. The exhaustive table of every variable lives in the [Configuration Reference](../reference/configuration.md).

## How configuration is read

Most variables use the `MEMORY_OS_` prefix and are read once at startup by `internal/config`. A number of subsystems additionally read **unprefixed aliases** (e.g. `REDIS_HOST` alongside `MEMORY_OS_REDIS_HOST`) or tuning knobs directly from the environment (e.g. `CHAIN_SIM_HIGH`).

!!! warning "Alias pairs: set both"
    A few knobs exist under two names read by different code paths, e.g. `STM_CACHE_MAX_TURNS` (read by the STM cache) and `MEMORY_OS_STM_CACHE_MAX_TURNS` (read by the config package). When in doubt, set both to the same value. The [Configuration Reference](../reference/configuration.md) flags every alias pair.

## The essentials

### Server

| Variable | Default | Purpose |
|---|---|---|
| `MEMORY_OS_PORT` | `8080` | REST (grpc-gateway) listen port |
| `MEMORY_OS_GRPC_PORT` | `9090` | gRPC listen port |
| `APP_ENV` | `local` | `local` \| `development` \| `staging` \| `production` |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

### LLM provider <span class="nx-badge nx-badge-required">Required</span>

| Variable | Default | Purpose |
|---|---|---|
| `LLM_PROVIDER` | `gemini` | `gemini` \| `azure` |
| `LLM_API_KEY` | — | API key for the selected provider |
| `LLM_MODEL_NAME` | `gemini-3-flash-preview` | Chat/completion model used by the pipeline |
| `EMBEDDING_MODEL_NAME` | `gemini-embedding-001` | Embedding model |
| `EMBEDDING_DIMENSIONS` | `1536` | **Must match the provider**: Azure `1536`, Gemini `768` |

### Datastores <span class="nx-badge nx-badge-required">Required</span>

| Variable | Default | Purpose |
|---|---|---|
| `MONGO_URI` | — | MongoDB connection string (STM durability + MTM chains) |
| `REDIS_HOST` / `REDIS_PORT` | `127.0.0.1` / `6379` | STM hot path + task queues |
| `MILVUS_HOST` / `MILVUS_PORT` | `127.0.0.1` / `19530` | MTM embedding search |
| `ARANGODB_URL` | `http://localhost:8529` | LTM knowledge graph |
| `ARANGODB_PASSWORD` | — | Server exits if LTM is enabled and this is empty |

### Blob storage <span class="nx-badge nx-badge-optional">Optional</span>

Needed only if agents store binary payloads via `StoreEvent`:

| Variable | Default | Purpose |
|---|---|---|
| `BLOB_PROVIDER` | `minio` | `minio` \| `s3` \| `gcs` |
| `BLOB_ENDPOINT` / `BLOB_BUCKET` | `localhost:9000` / `athena-blobs` | Where payloads land |
| `BLOB_ACCESS_KEY` / `BLOB_SECRET_KEY` | — | Credentials |

### Authentication <span class="nx-badge nx-badge-optional">Off by default</span>

All auth is **disabled by default**. That is fine for local development, never for production:

| Variable | Default | Purpose |
|---|---|---|
| `MEMORY_OS_REQUIRE_API_KEY` | `false` | Enforce `X-API-Key` / `Authorization: Bearer` |
| `MEMORY_OS_REQUIRE_JWT` | `false` | Enforce JWT with `tenant_id`/`user_id` claims |
| `MEMORY_OS_REQUIRE_MTLS` | `false` | Enforce client certificates |

See [Enabling Authentication](../guides/authentication.md) and the [Security Model](../concepts/security-model.md).

### Background workers

| Variable | Default | Purpose |
|---|---|---|
| `ENABLE_MEMORY_WORKERS` | `true` | Master switch for the cognitive pipeline |
| `MEMORY_WORKER_COUNT` | `2` | Worker goroutines consuming the task queue |

Turning workers off makes Athena a plain STM store: nothing is summarized, scored, or promoted. Details in [Workers & Schedulers](../guides/workers-and-schedulers.md).

## Pipeline tuning knobs

The chain-break thresholds (`CHAIN_SIM_HIGH`/`CHAIN_SIM_LOW`), heat-model parameters (`HEAT_*`), quality gate (`MTM_QUALITY_MIN_SCORE`), and merge gates (`SESSION_*`) all have sensible defaults and are explained where the algorithms are documented: [The Cognitive Pipeline](../concepts/cognitive-pipeline.md), [Heat & Decay](../concepts/heat-and-decay.md), and [Chain Formation](../concepts/chain-formation.md). Change them only after reading those pages.
