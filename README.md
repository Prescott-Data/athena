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

## 🔐 Authentication

Memory OS supports multiple authentication layers:

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
     -d '{"user_id": "user123", "metadata": {"app": "docintel"}}'
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

## 🛠️ Go Client SDK

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/dromos-org/memory-os/pkg/memoryos"
)

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
| `MEMORY_OS_MONGODB_URI` | MongoDB connection string | Required |
| `MEMORY_OS_REDIS_HOST` | Redis host | Required |
| `MEMORY_OS_MILVUS_HOST` | Milvus host | Required |

### Memory Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_OS_STM_CACHE_MAX_TURNS` | STM cache size | `10` |
| `MEMORY_OS_STM_CACHE_TTL_HOURS` | STM cache TTL | `2` |
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
      - MEMORY_OS_MONGODB_URI=${MONGODB_URI}
      - MEMORY_OS_REDIS_HOST=${REDIS_HOST}
      - MEMORY_OS_MILVUS_HOST=${MILVUS_HOST}
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

## 🔧 Development

### Running Tests
```bash
go test ./...
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
