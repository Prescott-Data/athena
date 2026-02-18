# Athena MemOS (Memory Operating System)

## 🧠 Overview

Athena MemOS is a standalone service designed to provide sophisticated, human-like memory capabilities to AI agents and applications. It addresses the challenge of limited context windows and statelessness in LLMs by implementing a multi-layered memory architecture. This allows an AI to remember, recall, and reason about past interactions, building a persistent understanding of its users and the world.

The core philosophy is to mimic the way human memory works, progressing from a fleeting short-term cache to consolidated mid-term memories, and finally to a permanent long-term knowledge base. This enables AI agents to engage in more meaningful, context-aware, and personalized conversations.

## 🏗️ Architecture

For a deep dive into the conceptual design and physics-based analogies (Kepler's Laws) governing our memory pipelines, see [The Orbital Mechanics of Memory](docs/ARCHITECTURE_EVOLUTION.md).

```
┌─────────────────────────────────────────────┐
│              Memory OS API                  │
├─────────────────────────────────────────────┤
│  REST/gRPC │ Auth │ Rate Limiting │ Metrics │
├─────────────────────────────────────────────┤
│           Memory Processing Layer            │
│    STM    │     MTM     │      LTM          │
│ (Cache)   │ (Segments)  │ (Knowledge Graph) │
├─────────────────────────────────────────────┤
│              Data Storage Layer             │
│ Redis │ MongoDB │ Milvus │ JanusGraph      │
└─────────────────────────────────────────────┘
```

## 🚀 Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- Access to memory infrastructure (Redis, MongoDB, Milvus, JanusGraph)

### Installation

1. **Clone and build:**
```bash
git clone <repository>
cd memory-os
go mod tidy
go build -o memory-server cmd/memory-server/main.go
```

2. **Configure environment:**
```bash
export MEMORY_OS_API_KEY="your-api-key"
export MEMORY_OS_JWT_SECRET="your-jwt-secret"
export MEMORY_OS_MONGODB_URI="mongodb://user:pass@host:27017/memory_os"
export MEMORY_OS_REDIS_HOST="your-redis-host"
export MEMORY_OS_MILVUS_HOST="your-milvus-host"
```

3. **Run the service:**
```bash
./memory-server
```

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
  - MongoDB collections (`dialogue_pages`, `segments`, `dialogue_chains`) use compound indexes on `tenantId,userId,agentId`.
  - Redis keys for STM cache and queues are scoped by `tenant_id:user_id:agent_id`.
  - Milvus collections include `tenantId,userId,agentId` fields for vector filtering.
  - JanusGraph nodes/edges include `tenantId` (and when applicable `userId`/`agentId`).

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

### Create Session (tenant/user enforced from auth)
```bash
curl -X POST http://localhost:8080/api/v1/sessions \
     -H "X-API-Key: your-api-key" \
     -H "Content-Type: application/json" \
     -d '{"user_id": "user123", "metadata": {"app": "docintel"}}'
```

### Store Interaction (IDs from context; client values ignored for security)
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
Retrieves the conversation segments for a session.
```bash
curl http://localhost:8080/api/v1/sessions/session-id/segments?limit=10 \
     -H "X-API-Key: your-api-key"
```

#### Get Heat Metrics
Retrieves the heat metrics for a session, indicating the level of engagement and importance.
```bash
curl http://localhost:8080/api/v1/sessions/session-id/analysis/heat \
     -H "X-API-Key: your-api-key"
```

#### Analyze Topics
Analyzes the conversation and returns a summary of the main topics discussed.
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
    
    	"bitbucket.org/dromos/memory-os/pkg/memoryos")

func main() {
    // Create client
    client := memoryos.NewClient(memoryos.ClientConfig{
        BaseURL:  "http://localhost:8080",
        APIKey:   "your-api-key",
        JWTToken: "your-jwt-token",
        Timeout:  30 * time.Second,
    })
    
    // Create session
    session, err := client.CreateSession(context.Background(), &memoryos.CreateSessionRequest{
        UserID: "user123",
        Metadata: map[string]string{"app": "docintel"},
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Store interaction
    _, err = client.StoreInteraction(context.Background(), session.SessionID, &memoryos.StoreInteractionRequest{
        UserMessage:   "Hello!",
        AgentResponse: "Hi there!",
        Timestamp:     time.Now(),
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Get context
    context, err := client.GetContext(context.Background(), session.SessionID, 10)
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Retrieved %d recent turns", len(context.RecentTurns))
}
```

## ⚙️ Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_OS_PORT` | HTTP server port | `8080` |
| `MEMORY_OS_GRPC_PORT` | gRPC server port | `9090` |
| `MEMORY_OS_API_KEY` | Valid API key | `default-api-key` |
| `MEMORY_OS_JWT_SECRET` | JWT signing secret | `your-secret-key` |
| `MONGO_URI` | MongoDB connection string | Required (e.g. `mongodb://user:pass@host:27017/memory_os?authSource=memory_os`) |
| `MONGO_DB` | MongoDB database name | `memory_os` |
| `REDIS_HOST` | Redis host | `localhost` |
| `REDIS_PORT` | Redis port | `6379` |
| `REDIS_PASSWORD` | Redis password | `` |
| `REDIS_DB` | Redis DB index | `0` (tests may use `3`) |
| `REDIS_POOL_SIZE` | Redis pool size | `10` |
| `REDIS_POOL_TIMEOUT` | Redis pool timeout (s) | `30` |
| `CACHE_TTL` | STM cache TTL (s) | `3600` |
| `AZURE_OPENAI_ENDPOINT` | Azure OpenAI endpoint | Required for embeddings |
| `AZURE_OPENAI_API_KEY` | Azure OpenAI API key | Required for embeddings |
| `MILVUS_HOST` | Milvus host | Optional (vector search disabled if missing) |
| `MILVUS_PORT` | Milvus port | `19530` |

### Memory Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `STM_CACHE_MAX_TURNS` | STM cache size | `10` |
| `STM_CACHE_TTL_HOURS` | STM cache TTL | `2` |
| `MEMORY_OS_MTM_QUALITY_MODE` | Quality validation mode | `balanced` |
| `MEMORY_OS_MTM_HEAT_THRESHOLD` | Heat promotion threshold | `0.8` |
| `MEMORY_OS_LTM_ENABLED` | Enable LTM features | `true` |

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
Prometheus metrics are available at `/metrics` (when enabled).

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

### Docker Compose
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
      - MONGO_URI=${MONGODB_URI}
      - MONGO_DB=memory_os
      - REDIS_HOST=${REDIS_HOST}
      - REDIS_PORT=${REDIS_PORT}
      - REDIS_PASSWORD=${REDIS_PASSWORD}
      - AZURE_OPENAI_ENDPOINT=${AZURE_OPENAI_ENDPOINT}
      - AZURE_OPENAI_API_KEY=${AZURE_OPENAI_API_KEY}
      - MILVUS_HOST=${MILVUS_HOST}
      - MILVUS_PORT=${MILVUS_PORT}
    restart: unless-stopped
```

## 🎯 Memory Types

### Short-Term Memory (STM) - *The Working Memory*

Of course. Here is a detailed explanation of how the Short-Term Memory (STM) works in this system, based on the code you and I have been working on.

#### High-Level Overview

The STM is designed to hold the most recent events in a conversation with a user. Its primary purpose is to provide immediate context for the AI agent. Think of it as the agent's "working memory." It's fast, volatile, and has a limited size.

The core of the STM functionality revolves around a few key components:

1.  **STM Cache (`stm_cache.go`)**: This is the actual storage mechanism for the STM. It uses Redis, an in-memory data store, to keep a list of recent conversational events for each user.
2.  **Task Queue (`task_queue.go`)**: When a new event happens (like a user sending a message), a task is added to a queue. This is an asynchronous way to trigger analysis of the conversation without blocking the main application flow.
3.  **Worker (`worker.go`)**: A background process that constantly watches the task queue. When a new task appears, the worker picks it up and performs the analysis.
4.  **STM Store (`stm_store.go`)**: This component provides the necessary tools for the worker to perform its analysis, such as creating vector embeddings and interacting with an LLM.

#### The Step-by-Step Workflow

Let's walk through what happens when a user sends a message:

**Step 1: A New Event is Added to the STM Cache**

1.  When the user sends a message, the application creates an `STMEvent` struct. This struct contains the content of the message, the role (e.g., "user"), the type of event (e.g., "message"), and a timestamp.
2.  This new `STMEvent` is then added to the user's STM cache in Redis using the `AddSTMEvent` function in `stm_cache.go`.
3.  The cache is implemented as a Redis list. The `LPUSH` command adds the new event to the beginning of the list, ensuring the most recent events are always at the front.
4.  To keep the memory from growing indefinitely, the list is trimmed to a maximum size (defined by `StmCacheMaxTurns`) using the `LTRIM` command. This creates a "sliding window" of the most recent conversational turns.
5.  A Time-To-Live (TTL) is also set on the Redis key, so if a conversation goes inactive for a long period, the memory is automatically cleared.

**Step 2: A Task is Enqueued using the "Index Queue" Pattern**

  This is where the new, scalable architecture comes into play. Instead of a single global queue, we use a "queue
  of queues".

   1. Immediately after the event is cached, the API server (server.go) begins the producer logic.
   2. It generates a scoped queue name that is unique to the agent (e.g.,
      'memory_processing_queue:v1:tenant-abc:user-123:agent-xyz').
   3. It creates a CognitiveChainCheckTask, which tells a worker to analyze the latest interaction for that specific
      agent.
   4. Two-Step Enqueue:
       * Step 2a: The task is pushed onto the agent's private, scoped queue using LPUSH.
       * Step 2b: The name of that scoped queue is then pushed onto the global work queue (cognitive_work_queue)
         using 'LPUSH'.

  This ensures that tasks for different agents are isolated, preventing a "Noisy Neighbor" scenario where a busy
  agent could block others.

  **Step 3: The Worker Processes the Task from the Index Queue**

   1. The Worker (worker.go), a constantly running background process, acts as the consumer.
   2. Two-Step Dequeue:
       * Step 3a: The worker performs a blocking pop (BRPOP) on the global work queue to get the name of a scoped
         queue that has work to be done.
       * Step 3b: The worker then performs a pop (RPop) on that scoped queue name to retrieve the actual
         CognitiveChainCheckTask.
   3. Once the task is retrieved, the worker calls the processCognitiveChainCheck function to perform the core
      analysis.

**Step 4: The "Cosine Gate" - Analyzing Conversational Flow**

This is the heart of the STM's intelligence, where the "cosine gate" logic comes into play.

1.  **Fetch Last Two Events**: The worker retrieves the two most recent events from the user's STM cache in Redis.
2.  **Create Vector Embeddings**: For each of the two events, the worker calls the `CreateEmbedding` function in `stm_store.go`. This function sends the text content of the event to an AI model (like Azure OpenAI's `text-embedding-ada-002`) which converts the text into a numerical representation called a vector embedding. These vectors capture the semantic meaning of the text.
3.  **Calculate Cosine Similarity**: The worker then calculates the [cosine similarity](https://en.wikipedia.org/wiki/Cosine_similarity) between the two vector embeddings. This calculation results in a score between -1 and 1, where 1 means the vectors are identical (the text has the same meaning), 0 means they are unrelated, and -1 means they are opposites.
4.  **Make a Decision**: The worker uses this similarity score to decide if the conversation is still on the same topic:
    *   **High Similarity (e.g., > 0.9)**: If the score is above a high threshold, the worker assumes the conversation is continuing smoothly. It does nothing and the task is complete.
    *   **Low Similarity (e.g., < 0.7)**: If the score is below a low threshold, the worker assumes a "chain break" has occurred—the user has changed the topic. It then proceeds to the next step (MTM formation).
    *   **Gray Area**: If the score is in between the high and low thresholds, the result is ambiguous. In this "gray area," the worker calls `analyzeTopicContinuity` in `stm_store.go`. This function sends the content of both events to a powerful Large Language Model (LLM) and asks it to make a final judgment on whether the topic has changed.

**Step 5: Medium-Term Memory (MTM) Formation (If a Chain Break Occurs)**

If a chain break is detected (either by low cosine similarity or by the LLM's decision), the worker initiates the process of forming a Medium-Term Memory:

1.  It retrieves the *entire* old conversation chain from the STM cache (all events before the most recent one).
2.  It calls `ProcessMTMFormation` in `stm_store.go`, which is responsible for summarizing the old conversation and storing it in a more permanent database (like MongoDB and Milvus) for long-term recall.
3.  Finally, it trims the STM cache in Redis, leaving only the single, most recent event, which becomes the start of a new conversational chain.

This entire process ensures that the agent has immediate access to the latest context while also being able to intelligently detect when a topic has changed and archive the old conversation for future reference. It's a sophisticated system for managing the flow of a conversation.

### Mid-Term Memory (MTM) - *Consolidating Experiences*
- **Analogy**: This is like the process of sleep, where the brain consolidates the day's important events into lasting memories.
- **Purpose**: To identify and preserve important, coherent topics from the STM. It groups related conversation turns into "segments," summarizes them, and scores them for importance.
- **Storage**: MongoDB stores the structured segments, and Milvus stores the vector embeddings of those segments for semantic search.
- **Process**: A background worker (the "Archivist") periodically scans the STM. It uses topic analysis and quality validation to find meaningful conversational arcs, which are then promoted to MTM as durable memories. Each segment gets a "heat score" to track its relevance over time.

### Long-Term Personal Memory (LTPM) - *The Knowledge Base*
- **Analogy**: This is the brain's permanent storage, holding facts, learned skills, and the core of our personality and identity.
- **Purpose**: To build a lasting, structured understanding of a user. It extracts key entities, facts, preferences, and personality traits from MTM segments.
- **Storage**: JanusGraph (a graph database) is used to store the complex relationships between entities, forming a rich knowledge graph. Key facts and persona models are also stored in MongoDB.
- **Process**: Another background worker (the "Promoter") analyzes high-heat segments from MTM. It extracts structured information (e.g., "User's favorite color is blue," "User is an expert in Python") and integrates it into the LTPM knowledge graph, creating a persistent user persona.

## 🔧 Development & Tests

### Running STM Tests

To run the Short-Term Memory (STM) tests, you will need a `.env.dev` file in the root of the project with the following variables:

```
MONGO_URI="mongodb://e2e_user:e2e_password_2024@localhost:27017/memory_os_e2e?authSource=admin"
MONGO_DBNAME="memory_os_e2e"
REDIS_HOST="localhost"
REDIS_PORT="6379"
REDIS_PASSWORD=""
REDIS_DB="0"
AZURE_OPENAI_ENDPOINT="https://dromos-open-ai.openai.azure.com/"
AZURE_OPENAI_API_KEY="..."
RUN_E2E_TESTS="true"
```

Once the `.env.dev` file is created, you can run the STM tests with the following command:

```bash
env $(grep -v '^#' .env.dev | xargs) go test -v ./internal/memory/...
```

This will run all the tests in the `internal/memory` directory with the environment variables loaded from the `.env.dev` file.

## 🧹 Development & Linting

### Quick linting
```bash
# Format, vet, and tidy
go fmt ./...
go vet ./...
go mod tidy
```

### Using helper script
```bash
# Run all checks (fmt, vet, tidy, golangci-lint if available, and tests)
./scripts/lint.sh all

# Or run specific steps
./scripts/lint.sh fmt
./scripts/lint.sh vet
./scripts/lint.sh tidy
./scripts/lint.sh lint   # requires golangci-lint
```

### Makefile targets
```bash
make lint       # runs all basic linting
make build      # builds memory-server
make test       # runs tests
make docker-build
```

### Optional: golangci-lint
```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b $(go env GOPATH)/bin v1.61.0
golangci-lint run
```

### Building
```bash
go build -o memory-server cmd/memory-server/main.go
```

### Generating Protobuf & API Docs (when needed)
If you modify `api/grpc/memory.proto`, you must regenerate the Go client/server code and the OpenAPI documentation.
```bash
./scripts/generate.sh
```

## 📚 API Documentation

This project uses OpenAPI (Swagger) for API documentation.

### Generating Documentation
The OpenAPI specification is generated automatically from the `api/grpc/memory.proto` file. To update it after making changes to the proto file, run:
```bash
./scripts/generate.sh
```
This will create/update the `docs/api/openapi.json` file.

### Viewing Documentation
You can use any OpenAPI-compatible viewer to see the interactive documentation. A popular choice is [Swagger UI](https://swagger.io/tools/swagger-ui/).

1.  Go to the public [Swagger Editor](https://editor.swagger.io/).
2.  Click `File` -> `Import File`.
3.  Upload the generated `docs/api/openapi.json` file.

This will give you a browsable, interactive UI for exploring and testing all the API endpoints.

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📄 License

[Add your license here]

---

**Memory OS**: Giving AI the gift of sophisticated memory 🧠✨
