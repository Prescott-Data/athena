# Memory Operating System (Memory OS)

## 🧠 Overview

The Memory Operating System is a standalone service that provides sophisticated memory capabilities for AI agents and applications. It implements a multi-layered memory architecture with Short-Term Memory (STM), Mid-Term Memory (MTM), and Long-Term Personal Memory (LTM) components.

## 🏗️ Architecture

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

### Short-Term Memory (STM)
- **Purpose**: Recent conversation cache
- **Storage**: Redis + MongoDB
- **TTL**: 2 hours (configurable)
- **Features**: Fast access, automatic cleanup

### Mid-Term Memory (MTM)
- **Purpose**: Important conversation segments
- **Storage**: MongoDB + Milvus (vectors)
- **Features**: Quality validation, heat scoring, topic analysis

### Long-Term Personal Memory (LTM)
- **Purpose**: User personality and knowledge graph
- **Storage**: JanusGraph + MongoDB
- **Features**: Persona modeling, relationship mapping

## 🔧 Development & Tests

### Running E2E Tests
Ensure infrastructure is reachable (Azure or local):
- MongoDB with authentication (example): `mongodb://memory_user:memory_password_2024@<host>:27017/memory_os?authSource=memory_os`
- Redis (set `REDIS_PASSWORD` if enabled)
- Milvus optional; if not set, vector search is disabled with a warning.

```bash
export MONGO_URI="mongodb://memory_user:memory_password_2024@<mongo-host>:27017/memory_os?authSource=memory_os"
export MONGO_DB=memory_os
export REDIS_HOST=<redis-host>
export REDIS_PORT=6379
export REDIS_PASSWORD=<redis-password>
export REDIS_DB=3

go test -v ./internal/memory -run "TestTenantIsolation|TestAuthValidation"
```

If Redis vars are empty, sensible defaults are applied to avoid test crashes.

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

### Generating Protobuf (when needed)
```bash
protoc --go_out=. --go-grpc_out=. --grpc-gateway_out=. api/grpc/memory.proto
```

## 📚 Documentation

- [Memory Architecture](MEMORY_ARCHITECTURE.md)
- [Quality Validator Implementation](QUALITY_VALIDATOR_IMPLEMENTATION.md)
- [API Documentation](docs/api/)
- [Deployment Guide](docs/deployment/)

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
