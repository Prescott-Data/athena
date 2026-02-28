# Athena MemOS (Memory Operating System)

## 🧠 Overview

Athena MemOS is a standalone service that provides sophisticated, human-like memory capabilities to AI agents and applications. It addresses the challenge of limited context windows and statelessness in LLMs by implementing a multi-layered memory architecture that allows an AI to remember, recall, and reason about past interactions, building a persistent understanding of its users and the world.

The core philosophy mimics the way human memory works, progressing from a fleeting short-term cache to consolidated mid-term memories, and finally to a permanent long-term knowledge graph. This enables AI agents to engage in more meaningful, context-aware, and personalized conversations.

## 🏗️ Architecture

For a deep dive into the conceptual design and physics-based analogies (Kepler's Laws) governing the memory pipelines, see [The Orbital Mechanics of Memory](docs/ARCHITECTURE_EVOLUTION.md).

```
┌────────────────────────────────────────────────────────────────┐
│                      Memory OS API                             │
├────────────────────────────────────────────────────────────────┤
│  REST/gRPC  │  Auth  │  Rate Limiting  │  Metrics (Prometheus) │
├────────────────────────────────────────────────────────────────┤
│                  Memory Processing Layer                       │
│  STM (Cache)  │  MTM (Cognitive Chains)  │  LTM (Knowledge)   │
├────────────────────────────────────────────────────────────────┤
│                    Data Storage Layer                           │
│  Redis  │  MongoDB  │  Milvus  │  ArangoDB  │  Blob (MinIO/S3)│
└────────────────────────────────────────────────────────────────┘
```

## 🚀 Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- An LLM provider (Azure OpenAI or Google Gemini)

### Local Infrastructure

Spin up all required databases with a single command:

```bash
docker compose -f docker-compose.local.yml up -d
```

This starts:
- **Redis** (port 6379) — STM cache & task queues
- **MongoDB** (port 27017) — MTM cognitive chains & events
- **Milvus** (port 19530) — Vector similarity search
- **ArangoDB** (port 8529) — LTM knowledge graph
- **MinIO** (ports 9000/9001) — Blob storage for heavy payloads

### Running the Server

```bash
# Option 1: Use the local dev script (pre-configured env vars)
bash ./run_local_server.sh

# Option 2: Build and run manually
go build -o memory-server cmd/memory-server/main.go
./memory-server
```

### Running the E2E Simulation

```bash
go run cmd/simulate/main.go
```

This executes 13 end-to-end scenarios covering session creation, interactions, recall, archival, and blob storage stress testing.

## 🔐 Authentication & Multi-Tenancy

Memory OS supports multiple authentication layers and enforces enterprise-grade multi-tenancy.

### API Key Authentication
```bash
curl -H "X-API-Key: your-api-key" \
     http://localhost:8080/api/v1/sessions
```

### JWT Authentication
```bash
curl -H "X-JWT-Token: your-jwt-token" \
     http://localhost:8080/api/v1/sessions/session-id/context
```

### Multi-Tenancy Enforcement

- **Hierarchy**: `tenant_id / user_id / agent_id`
- **Source of truth**: `tenant_id` and `user_id` are enforced from authentication (JWT/API key) and injected into request context. Client-supplied values are ignored.
- **Isolation**:
  - MongoDB collections use compound indexes on `tenantId,userId,agentId`.
  - Redis keys for STM cache and queues are scoped by `tenant_id:user_id:agent_id`.
  - Milvus collections include `tenantId,userId,agentId` fields for vector filtering.
  - ArangoDB graph nodes include `tenantId` for knowledge graph isolation.

#### Required JWT Claims
- `tenant_id` (string)
- `user_id` (string)

If using API keys, the service resolves `tenant_id` and `user_id` from the key configuration.

### mTLS (Optional)
For production deployments, enable mutual TLS:
```bash
export MEMORY_OS_ENABLE_TLS=true
export MEMORY_OS_TLS_CERT_FILE="/path/to/server.crt"
export MEMORY_OS_TLS_KEY_FILE="/path/to/server.key"
export MEMORY_OS_CLIENT_CA_CERT_FILE="/path/to/ca.crt"
```

## 📡 API Usage

### Create Session
```bash
curl -X POST http://localhost:8080/api/v1/sessions \
     -H "X-API-Key: your-api-key" \
     -H "Content-Type: application/json" \
     -d '{"user_id": "user123", "metadata": {"app": "myapp"}}'
```

### Store Interaction
```bash
curl -X POST http://localhost:8080/api/v1/sessions/session-id/interactions \
     -H "X-API-Key: your-api-key" \
     -H "Content-Type: application/json" \
     -d '{
       "user_message": "Hello, how are you?",
       "agent_response": "Hi! I am doing well, thank you for asking.",
       "timestamp": "2024-01-01T12:00:00Z"
     }'
```

### Get Context
```bash
curl http://localhost:8080/api/v1/sessions/session-id/context?limit=10 \
     -H "X-API-Key: your-api-key"
```

### Search Memory
```bash
curl -X POST http://localhost:8080/api/v1/sessions/session-id/context/search \
     -H "X-API-Key: your-api-key" \
     -H "Content-Type: application/json" \
     -d '{
       "query": "machine learning concepts",
       "limit": 5,
       "similarity_threshold": 0.7
     }'
```

### Intelligence API

#### Get Segments
```bash
curl http://localhost:8080/api/v1/sessions/session-id/segments?limit=10 \
     -H "X-API-Key: your-api-key"
```

#### Get Heat Metrics
```bash
curl http://localhost:8080/api/v1/sessions/session-id/analysis/heat \
     -H "X-API-Key: your-api-key"
```

#### Analyze Topics
```bash
curl http://localhost:8080/api/v1/sessions/session-id/analysis/topics \
     -H "X-API-Key: your-api-key"
```

## 🛠️ Go Client SDK

```go
package main

import (
    "context"
    "log"
    "time"

    "bitbucket.org/dromos/memory-os/pkg/memoryos"
)

func main() {
    client := memoryos.NewClient(memoryos.ClientConfig{
        BaseURL:  "http://localhost:8080",
        APIKey:   "your-api-key",
        JWTToken: "your-jwt-token",
        Timeout:  30 * time.Second,
    })

    // Create session
    session, err := client.CreateSession(context.Background(), &memoryos.CreateSessionRequest{
        UserID:   "user123",
        Metadata: map[string]string{"app": "myapp"},
    })
    if err != nil { log.Fatal(err) }

    // Store interaction
    _, err = client.StoreInteraction(context.Background(), session.SessionID, &memoryos.StoreInteractionRequest{
        UserMessage:   "Hello!",
        AgentResponse: "Hi there!",
        Timestamp:     time.Now(),
    })
    if err != nil { log.Fatal(err) }

    // Get context
    ctx, err := client.GetContext(context.Background(), session.SessionID, 10)
    if err != nil { log.Fatal(err) }

    log.Printf("Retrieved %d recent turns", len(ctx.RecentTurns))
}
```

## ⚙️ Configuration

### Core Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_OS_PORT` | HTTP server port | `8080` |
| `MEMORY_OS_GRPC_PORT` | gRPC server port | `9090` |
| `MEMORY_OS_API_KEY` | Valid API key | `default-api-key` |
| `MEMORY_OS_JWT_SECRET` | JWT signing secret | `your-secret-key` |

### Database Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_OS_MONGODB_URI` | MongoDB connection string | Required |
| `MEMORY_OS_MONGODB_DATABASE` | MongoDB database name | `memory_os` |
| `MEMORY_OS_REDIS_HOST` | Redis host | `localhost` |
| `REDIS_PORT` | Redis port | `6379` |
| `REDIS_PASSWORD` | Redis password | `` |
| `REDIS_DB` | Redis DB index | `0` |
| `REDIS_POOL_SIZE` | Redis pool size | `10` |
| `CACHE_TTL` | STM cache TTL (seconds) | `3600` |
| `MILVUS_HOST` | Milvus host | Optional |
| `MILVUS_PORT` | Milvus port | `19530` |
| `ARANGODB_URL` | ArangoDB connection URL | `http://localhost:8529` |
| `ARANGODB_USER` | ArangoDB username | `root` |
| `ARANGODB_PASSWORD` | ArangoDB password | Required |
| `ARANGODB_DATABASE` | ArangoDB database name | `athena_ltm` |

### LLM Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `LLM_PROVIDER` | LLM backend (`azure` or `gemini`) | `azure` |
| `AZURE_OPENAI_ENDPOINT` | Azure OpenAI endpoint | Required if `azure` |
| `AZURE_OPENAI_API_KEY` | Azure OpenAI API key | Required if `azure` |
| `GEMINI_API_KEY` | Gemini API key | Required if `gemini` |
| `LLM_TIMEOUT_SECONDS` | LLM request timeout | `10` |
| `LLM_RATE_LIMIT_PER_MINUTE` | LLM rate limit | `50` |

### Blob Storage Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `BLOB_PROVIDER` | Blob storage provider (`minio`, `s3`, `gcs`, `azure`) | _(disabled if unset)_ |
| `BLOB_ENDPOINT` | Blob storage endpoint | `localhost:9000` |
| `BLOB_BUCKET` | Blob storage bucket name | `athena-blobs` |
| `BLOB_ACCESS_KEY` | Blob storage access key | Required |
| `BLOB_SECRET_KEY` | Blob storage secret key | Required |
| `BLOB_USE_SSL` | Enable TLS for blob storage | `false` |
| `BLOB_REGION` | Blob storage region | `us-east-1` |

### Memory Tuning

| Variable | Description | Default |
|----------|-------------|---------|
| `STM_CACHE_MAX_TURNS` | Max events in STM sliding window | `10` |
| `STM_CACHE_TTL` | STM cache TTL (hours) | `2` |
| `CHAIN_SIM_HIGH` | Cosine similarity upper threshold | `0.72` |
| `CHAIN_SIM_LOW` | Cosine similarity lower threshold | `0.52` |
| `HEAT_DECAY_TAU_HOURS` | Heat decay time constant | `24.0` |
| `MEMORY_OS_MTM_QUALITY_MODE` | Quality validation mode | `balanced` |
| `MEMORY_OS_MTM_HEAT_THRESHOLD` | Heat promotion threshold | `0.8` |
| `MEMORY_OS_PROMOTER_INTERVAL_MIN` | Promoter cycle interval (minutes) | `5` |
| `MEMORY_OS_PROMOTER_THRESHOLD` | LTM promotion heat threshold | `0.1` |
| `MEMORY_OS_ARCHIVER_INTERVAL_MIN` | Archiver cycle interval (minutes) | `5` |
| `MTM_ARCHIVE_SCAN_DAYS` | Days before a chain is considered cold | `7` |
| `MTM_FREEZING_POINT` | Heat score below which a chain is archived | `0.1` |

## 🎯 Memory Types

### Short-Term Memory (STM) — The Working Memory

The STM holds the most recent events in a conversation. Its primary purpose is to provide immediate context to the AI agent. Think of it as the agent's "working memory" — fast, volatile, and size-limited.

**Core components:**

| Component | File | Role |
|-----------|------|------|
| **STM Cache** | `stm_cache.go` | Redis-backed sliding window of recent events |
| **Task Queue** | `task_queue.go` | Async "index queue" pattern for background analysis |
| **Worker** | `worker.go` | Background consumer that performs cosine-gate analysis |
| **STM Store** | `stm_store.go` | Embeddings, LLM analysis, and MTM formation |

**Workflow:**

1. **Event Ingestion** — A new `STMEvent` is `LPUSH`'d into a Redis list and trimmed to `STM_CACHE_MAX_TURNS`. A TTL ensures inactive conversations self-clean.
2. **Scoped Task Queue** — The server enqueues a `CognitiveChainCheckTask` to an agent-specific queue (preventing noisy-neighbor effects), then registers that queue on the global `cognitive_work_queue`.
3. **Worker Processing** — A worker `BRPOP`s the global queue, discovers the scoped queue, and pops the actual task.
4. **Cosine Gate** — The worker embeds the two most recent events and calculates cosine similarity:
   - **High similarity (> `CHAIN_SIM_HIGH`)**: Conversation continues. No action.
   - **Low similarity (< `CHAIN_SIM_LOW`)**: Topic shift detected → triggers MTM formation.
   - **Gray area**: An LLM arbitrates topic continuity.
5. **MTM Formation** — On chain break, the old chain is flushed to MongoDB/Milvus via `ProcessMTMFormation`, and the STM cache resets to only the latest event.

### Mid-Term Memory (MTM) — Consolidating Experiences

MTM is analogous to sleep-based memory consolidation. It identifies and preserves important, coherent topics from the STM by grouping conversation turns into **cognitive chains**, summarizing them, and scoring them for importance.

**Storage:**
- **MongoDB** stores structured cognitive chains in the `cognitive_chains` and `cognitive_events` collections.
- **Milvus** stores vector embeddings of chain summaries for semantic search.

**Key mechanisms:**
- **Heat Scoring** — An Ebbinghaus-inspired decay curve with spaced-repetition cooldowns. Chains that are repeatedly recalled over meaningful time intervals maintain high heat; chains that are never revisited decay toward absolute zero.
- **Quality Validation** — Each chain is assessed for coherence, information density, and contextual relevance before promotion.
- **Cold Chain Archival** — A background Archiver scans for chains older than `MTM_ARCHIVE_SCAN_DAYS` with heat scores below `MTM_FREEZING_POINT`. Archived chains have their Milvus vectors deleted and MongoDB status set to `archived`. Associated blob objects are also garbage-collected from the Blob Store.

### Long-Term Memory (LTM) — The Knowledge Graph

LTM is the agent's permanent storage — facts, preferences, relationships, and personality traits extracted from consolidated MTM chains.

**Storage:** **ArangoDB** — a native multi-model graph database that stores entities (Identities, Concepts, Tools, Topics, Locations) and weighted edges (KNOWS, USES, INTERESTED_IN, RELATES_TO, etc.) forming a rich knowledge graph.

**Key mechanisms:**
- **Promoter** — A background scheduler that scans cognitive chains exceeding the promotion threshold. For qualifying chains, a `GraphExtractor` uses LLM reasoning to extract structured entity-relationship triples from chain summaries.
- **LTM Writer** — Receives extracted triples and performs AQL `UPSERT` operations against ArangoDB. Repeated edges accumulate weight and average their confidence scores ("Balanced Merge").
- **Ontology** — The graph schema supports typed entity collections and labeled edge types. Unknown relationships fall back to `RELATES_TO` with a `context_nuance` property to preserve semantic meaning.

### Blob Storage — The Claim Check Pattern

For heavy payloads (large JSON dumps, raw logs, binary files), Athena implements the **Claim Check Pattern** to keep Redis and MongoDB lightweight.

**How it works:**
1. When a `StoreEvent` request carries a binary `Payload` or a `MimeType` header, the server streams the data directly to the configured Blob Store (MinIO locally, S3/GCS/Azure in production).
2. Only the resulting `BlobURI` (e.g., `s3://athena-blobs/events/abc123`) and `BlobMimeType` are stored in the `CognitiveEvent` and `STMEvent` structs.
3. During cold chain archival, the Archiver also deletes orphaned blobs to prevent runaway storage costs.

**Supported providers:**

| Provider | `BLOB_PROVIDER` value | Notes |
|----------|----------------------|-------|
| MinIO | `minio` | Local development (S3-compatible) |
| AWS S3 | `s3` | Production |
| Google Cloud Storage | `gcs` | Stub (coming soon) |
| Azure Blob Storage | `azure` | Stub (coming soon) |

## 🏥 Health Monitoring

### Health Check
```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "status": "healthy",
  "timestamp": "2024-01-01T12:00:00Z",
  "service": "memory-os"
}
```

### Metrics
Prometheus metrics are available at `/metrics`.

## 🚢 Deployment

### Docker
```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o memory-server cmd/memory-server/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/memory-server .
CMD ["./memory-server"]
```

### Docker Compose (Production)
```yaml
version: '3.8'
services:
  memory-os:
    build: .
    ports:
      - "8080:8080"
      - "9090:9090"
    environment:
      - MEMORY_OS_API_KEY=${API_KEY}
      - MEMORY_OS_JWT_SECRET=${JWT_SECRET}
      - MEMORY_OS_MONGODB_URI=${MONGODB_URI}
      - MEMORY_OS_MONGODB_DATABASE=memory_os
      - MEMORY_OS_REDIS_HOST=${REDIS_HOST}
      - REDIS_PORT=${REDIS_PORT}
      - REDIS_PASSWORD=${REDIS_PASSWORD}
      - MILVUS_HOST=${MILVUS_HOST}
      - MILVUS_PORT=${MILVUS_PORT}
      - ARANGODB_URL=${ARANGODB_URL}
      - ARANGODB_USER=${ARANGODB_USER}
      - ARANGODB_PASSWORD=${ARANGODB_PASSWORD}
      - ARANGODB_DATABASE=${ARANGODB_DATABASE}
      - BLOB_PROVIDER=${BLOB_PROVIDER}
      - BLOB_ENDPOINT=${BLOB_ENDPOINT}
      - BLOB_BUCKET=${BLOB_BUCKET}
      - BLOB_ACCESS_KEY=${BLOB_ACCESS_KEY}
      - BLOB_SECRET_KEY=${BLOB_SECRET_KEY}
      - LLM_PROVIDER=${LLM_PROVIDER}
    restart: unless-stopped
```

## 🔧 Development

### Running Tests
```bash
# Unit tests
go test ./...

# Memory integration tests (requires local infrastructure)
env $(grep -v '^#' .env.dev | xargs) go test -v ./internal/memory/...
```

### Linting
```bash
go fmt ./...
go vet ./...
go mod tidy

# Or use the helper script
./scripts/lint.sh all
```

### Makefile Targets
```bash
make lint       # runs all basic linting
make build      # builds memory-server
make test       # runs tests
make generate   # regenerate protobuf & OpenAPI docs
```

### Generating Protobuf & API Docs
If you modify `api/grpc/memory.proto`, regenerate the Go client/server code and OpenAPI documentation:
```bash
make generate
```

## 📚 API Documentation

This project uses OpenAPI (Swagger) for API documentation, auto-generated from the `api/grpc/memory.proto` file.

The specification lives at `docs/api/openapi.json`. View it interactively at [Swagger Editor](https://editor.swagger.io/) by importing the file.

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📄 License

[Add your license here]

---

**Athena MemOS**: Giving AI the gift of sophisticated memory 🧠✨
