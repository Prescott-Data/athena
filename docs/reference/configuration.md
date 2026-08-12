# Configuration Reference

Every environment variable the code reads, by category. Defaults are the code's defaults; `.env.example` sometimes ships different (more conservative) values, noted where it matters. Variables marked <span class="nx-badge nx-badge-required">Required</span> cause startup failure when missing.

!!! warning "Alias pairs"
    Some settings exist under two names read by different code paths. Alias rows are marked **(alias)**; when in doubt, set both names to the same value.

## Server

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_PORT` | `8080` | REST (grpc-gateway) listen port |
| `MEMORY_OS_GRPC_PORT` | `9090` | gRPC listen port |
| `MEMORY_OS_READ_TIMEOUT` | | HTTP read timeout |
| `MEMORY_OS_WRITE_TIMEOUT` | | HTTP write timeout |
| `MEMORY_OS_ENABLE_TLS` | `false` | Serve TLS |
| `MEMORY_OS_TLS_CERT_FILE` / `MEMORY_OS_TLS_KEY_FILE` | | Server certificate and key paths |
| `MEMORY_OS_CLIENT_CA_CERT_FILE` | | CA bundle for verifying client certs (mTLS) |

## Authentication

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_REQUIRE_API_KEY` | `false` | Enforce API-key auth |
| `MEMORY_OS_API_KEY` | | The accepted key |
| `MEMORY_OS_REQUIRE_JWT` | `false` | Enforce JWT auth |
| `MEMORY_OS_JWT_SECRET` | | HMAC secret for token validation |
| `MEMORY_OS_REQUIRE_MTLS` | `false` | Enforce client certificates |

Recipes: [Enabling Authentication](../guides/authentication.md).

## Redis (STM hot path, queues)

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_REDIS_HOST` / `REDIS_HOST` (alias) | `localhost` | |
| `MEMORY_OS_REDIS_PORT` / `REDIS_PORT` (alias) | `6379` | |
| `MEMORY_OS_REDIS_PASSWORD` / `REDIS_PASSWORD` (alias) | | |
| `MEMORY_OS_REDIS_DB` / `REDIS_DB` (alias) | `0` | |
| `REDIS_POOL_SIZE` | `10` | Connection pool size |
| `REDIS_POOL_TIMEOUT` | `30` | Pool wait timeout (seconds) |
| `CACHE_TTL` | `3600` | Generic cache TTL (seconds) |

## MongoDB (events, chains, sessions)

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_MONGODB_URI` / `MONGO_URI` (alias) | | Connection string <span class="nx-badge nx-badge-required">Required</span> |
| `MEMORY_OS_MONGODB_DATABASE` / `MONGO_DB` (alias) | | Database name |
| `MEMORY_OS_MONGODB_USERNAME` | | |
| `MEMORY_OS_MONGODB_PASSWORD` | | <span class="nx-badge nx-badge-required">Required</span> |

## Milvus (chain embeddings)

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_MILVUS_HOST` / `MILVUS_HOST` (alias) | `localhost` | |
| `MEMORY_OS_MILVUS_PORT` / `MILVUS_PORT` (alias) | `19530` | |
| `MILVUS_VECTOR_DIMENSION` | | Overrides vector dimension; normally derive from `EMBEDDING_DIMENSIONS` |

## ArangoDB (LTM graph)

| Variable | Default | Description |
|---|---|---|
| `ARANGODB_URL` | `http://localhost:8529` | Full URL; takes precedence |
| `ARANGODB_HOST` / `ARANGODB_PORT` | | Fallback when no URL |
| `ARANGODB_USER` | `root` | |
| `ARANGODB_PASSWORD` | | <span class="nx-badge nx-badge-required">Required</span> when LTM enabled |
| `ARANGODB_DATABASE` | `athena_ltm` | |
| `MEMORY_OS_LTM_ENABLED` | `true` | Master switch for the LTM tier |
| `MEMORY_OS_LTM_KG_ENABLED` | `true` | Knowledge-graph writes on/off |
| `MEMORY_OS_LTM_PERSONA_THRESHOLD` | | Persona promotion threshold (reserved) |

## LLM and embeddings

| Variable | Default | Description |
|---|---|---|
| `LLM_PROVIDER` | `azure` | `gemini` \| `azure` \| `openai` <span class="nx-badge nx-badge-required">Required</span> |
| `LLM_API_KEY` | | Provider key <span class="nx-badge nx-badge-required">Required</span>; works for every provider |
| `GEMINI_API_KEY` / `AZURE_OPENAI_API_KEY` / `OPENAI_API_KEY` | | Provider-native fallbacks when `LLM_API_KEY` is unset |
| `LLM_MODEL_NAME` | per provider | Completion model (`gemini-1.5-pro` / `gpt-4`) |
| `EMBEDDING_MODEL_NAME` | per provider | Embedding model (`gemini-embedding-001` / `text-embedding-ada-002`) |
| `MILVUS_VECTOR_DIMENSION` | `1536` | **Must match the embedding model**: ada-002 1536, gemini-embedding-001 3072 ([the trap](../guides/llm-providers.md#the-dimension-trap)) |
| `AZURE_OPENAI_ENDPOINT` | | Azure endpoint fallback when `LLM_BASE_URL` is unset |
| `LLM_BASE_URL` / `EMBEDDING_BASE_URL` | | Azure deployment URLs (required for `azure`); custom base URL for `openai` |
| `LLM_TIMEOUT_SECONDS` | `10` | General per-call timeout |
| `LLM_SUMMARY_TIMEOUT_SEC` | | Summarization call timeout |
| `LLM_EMBEDDING_TIMEOUT_SEC` | | Embedding call timeout |
| `LLM_RATE_LIMIT_PER_MINUTE` | `50` | Pipeline-wide rate cap |
| `LLM_CIRCUIT_BREAKER_THRESHOLD` | `5` | Consecutive failures to open the breaker |
| `LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS` | `60` | Breaker open duration |

## Blob storage

| Variable | Default | Description |
|---|---|---|
| `BLOB_PROVIDER` | `minio` | `minio` \| `s3` \| `gcs` \| `azure` |
| `BLOB_ENDPOINT` | | Endpoint (MinIO/S3-compatible) |
| `BLOB_BUCKET` / `BLOB_CONTAINER` | | Bucket (S3/GCS/MinIO) or container (Azure) |
| `BLOB_ACCESS_KEY` / `BLOB_SECRET_KEY` | | Credentials |
| `BLOB_CONNECTION_STRING` | | Azure connection string |
| `BLOB_REGION` | `us-east-1` | |
| `BLOB_USE_SSL` | `false` | |

## Workers and schedulers

| Variable | Default | Description |
|---|---|---|
| `ENABLE_MEMORY_WORKERS` | `true` | Master switch for the cognitive pipeline |
| `MEMORY_WORKER_COUNT` / `MEMORY_OS_NUM_WORKERS` (alias) | `2` | Worker goroutines |
| `MEMORY_OS_PROMOTER_INTERVAL_MIN` | `30` | Promoter ticker (minutes) |
| `MEMORY_OS_PROMOTER_THRESHOLD` | `0.1` | Minimum heat for promoter consideration |
| `MEMORY_OS_ARCHIVER_INTERVAL_MIN` | `60` | Archiver ticker (minutes) |

## STM cache

| Variable | Default | Description |
|---|---|---|
| `STM_CACHE_MAX_TURNS` / `MEMORY_OS_STM_CACHE_MAX_TURNS` (alias) | `10` | Sliding-window size |
| `STM_CACHE_TTL` / `MEMORY_OS_STM_CACHE_TTL_HOURS` (alias) | `2` | Window TTL (hours) |
| `STM_CACHE_KEY_PREFIX` | `stm` | Redis key prefix |
| `STM_TASK_TIMEOUT` | `300` | Per-task budget (seconds) |
| `STM_MAX_EVENTS_BEFORE_FLUSH` | `20` | Force-flush threshold |

## Chain-break detection

| Variable | Default | Description |
|---|---|---|
| `CHAIN_SIM_HIGH` | `0.72` | Similarity at/above: same topic |
| `CHAIN_SIM_LOW` | `0.52` | Similarity below: topic break |

Between the two: LLM arbitration ([The Cognitive Pipeline](../concepts/cognitive-pipeline.md)).

## Heat model

| Variable | Default | Description |
|---|---|---|
| `HEAT_WEIGHT_INTRINSIC` | `0.7` | Weight of LLM importance in base importance |
| `HEAT_WEIGHT_DENSITY` | `0.3` | Weight of structural density |
| `HEAT_DECAY_TAU_HOURS` | `24` | Decay constant τ |
| `HEAT_RECALL_GROWTH` | `1.5` | Recall-strength multiplier per spaced access |
| `HEAT_COOLDOWN_HOURS` | `12` | Minimum gap between recall boosts |
| `MEMORY_OS_MTM_HEAT_THRESHOLD` | `0.3` | Promotion threshold (`.env.example` ships `0.8`) |

!!! warning "Dead variable"
    `HEAT_WEIGHT_COGNITIVE` appears in `.env.example` but is **not read by the code**. Use `HEAT_WEIGHT_DENSITY`.

## MTM quality, merging, topic analysis

| Variable | Default | Description |
|---|---|---|
| `MEMORY_OS_MTM_QUALITY_MODE` | `balanced` | `fast` \| `balanced` \| `thorough` |
| `MTM_QUALITY_MIN_SCORE`* | `0.5` | Quality-gate floor |
| `MEMORY_OS_MTM_MERGE_THRESHOLD` | `0.85` | Merge confidence cap |
| `MEMORY_OS_MTM_MAX_SEGMENTS` | `50` | Max segments per formation |
| `SESSION_MERGE_MIN_CONFIDENCE` | `0.7` | Merge gate: confidence |
| `SESSION_SIMILARITY_THRESHOLD` | `0.6` | Merge gate: similarity |
| `SESSION_MAX_AGE_HOURS` | `72` | Merge-candidate window |
| `SESSION_MAX_CHAINS_PER_USER` | `100` | Candidate cap |
| `SESSION_KEYWORD_WEIGHT` | | Keyword weighting in candidate scoring |
| `SESSION_VECTOR_SEARCH_LIMIT` | | Candidate vector-search size |
| `TOPIC_ANALYSIS_MAX_TOPICS` | `3` | Max subtopics |
| `TOPIC_ANALYSIS_MIN_CONFIDENCE` | `0.6` | Topic confidence floor |
| `TOPIC_ANALYSIS_LLM_TIMEOUT` | `20` | Topic-analysis timeout (seconds) |
| `TOPIC_ANALYSIS_KEYWORD_BOOST` | `true` | Keyword weighting |
| `CONTINUITY_CONFIDENCE_THRESHOLD` | `0.8` | Continuity analyzer gate |
| `CONTINUITY_SEMANTIC_THRESHOLD` | | Semantic continuity gate |
| `CONTINUITY_MAX_TIME_HOURS` | | Max gap treated as continuous |
| `CONTINUITY_LLM_TIMEOUT_SECONDS` | | Continuity LLM timeout |

*`MTM_QUALITY_MIN_SCORE` is read by the quality validator; see [Chain Formation](../concepts/chain-formation.md).

## Archiver

| Variable | Default | Description |
|---|---|---|
| `MTM_ARCHIVE_SCAN_DAYS` | `7` | Idle days before a chain is an archive candidate |
| `MTM_FREEZING_POINT` | `0.1` | Heat below which candidates are archived |

## Variables in `.env.example` that the code does not read

`PORT`, `APP_ENV`, `LOG_LEVEL`, `NODE_ENV`, `ALLOWED_ORIGINS`, `HEAT_WEIGHT_COGNITIVE`, and the legacy `*_LEGACY`/`*_API_VERSION` Azure variables are present in the template but have no effect on the current build. Setting them is harmless; relying on them is a bug.
