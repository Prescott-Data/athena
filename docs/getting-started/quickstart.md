# Deploy in Five Minutes

Run the full Athena stack (server plus all five infrastructure dependencies) on your local machine.

## Prerequisites

- **Go 1.26+**
- **Docker** and **Docker Compose**
- **protoc** (only needed if you plan to regenerate the API from proto via `make generate`)
- Free ports: `8080` (REST), `9090` (gRPC), `6379` (Redis), `27017` (MongoDB), `19530` (Milvus), `8529` (ArangoDB), `9000`/`9001` (MinIO)

## 1. Configure the environment

Copy the template and fill in the four required secrets. The server exits on startup if any of them is empty.

```bash
git clone https://github.com/Prescott-Data/athena.git
cd athena
cp .env.example .env.dev
```

```bash title=".env.dev: the four required values"
LLM_PROVIDER=gemini                      # gemini | azure
LLM_API_KEY=<your key>                   # or AZURE_OPENAI_API_KEY for azure
MEMORY_OS_MONGODB_PASSWORD=admin123      # matches docker-compose.local.yml
ARANGODB_PASSWORD=athena_dev             # matches docker-compose.local.yml
```

!!! warning "Embedding dimensions differ per provider"
    Azure OpenAI embeddings are **1536**-dimensional; Gemini embeddings are **768**-dimensional. The Milvus collection is created with `EMBEDDING_DIMENSIONS`; switching providers later requires dropping and recreating the collection. See [LLM Providers](../guides/llm-providers.md).

## 2. Start the infrastructure

```bash
docker compose -f docker-compose.local.yml up -d
```

This boots six containers:

| Service | Port | Role |
|---|---|---|
| Redis 7 | 6379 | STM hot path + task queue |
| MongoDB 7 | 27017 | STM durability + MTM chains |
| Milvus v2.3.4 | 19530 | MTM vector search |
| etcd v3.5 | 2379 | Milvus coordination |
| MinIO | 9000/9001 | Blob storage (local S3) |
| ArangoDB | 8529 | LTM knowledge graph |

Give it 10–15 seconds. ArangoDB and Milvus take the longest to become ready.

## 3. Initialize the graph schema

The ArangoDB `athena_ltm` database, its vertex collections, and the `MemoryEdges` edge collection must exist before the server starts:

```bash
go run cmd/init-ltm/main.go
```

!!! tip "Connection refused?"
    ArangoDB is still booting. Wait ten seconds and run it again; the command is idempotent.

## 4. Start the server

```bash
go run cmd/memory-server/main.go
```

Watch the log output: the server connects to all five dependencies, starts its background workers, and listens on REST `:8080` and gRPC `:9090`.

## 5. Verify

```bash
curl http://localhost:8080/health
```

```json
{"status": "healthy", "service": "memory-os", "timestamp": "..."}
```

You have a running Memory OS. Continue to [Your First Session](first-session.md) to store and retrieve your first memory, or review [Configuration](configuration.md) for what you just wired together.

## Running tests

```bash
make test-short   # unit tests, no Docker needed (databases are mocked)
make test-e2e     # full integration suite; needs the Docker stack AND a real LLM key
```

!!! note
    `make test-e2e` loads `.env.dev` and makes real calls to your configured LLM provider.
