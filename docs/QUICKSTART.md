# Quickstart — Getting Athena Running Locally

This guide will get the Athena MemOS server and all its infrastructure databases running on your local machine in under 15 minutes.

---

## 1. Prerequisites

You need the following installed:
- **Go 1.26+**
- **Docker** and **Docker Compose**
- **protoc** (Protocol Buffers compiler): `brew install protobuf`
- Make sure ports `8080`, `9090`, `6379`, `27017`, `19530`, `8529`, and `9000` are free.

---

## 2. Environment Setup

1. Clone the repository and navigate to the `athena-memos` directory:
   ```bash
   cd dromos-core/athena-memos
   ```

2. Copy the example environment file:
   ```bash
   cp .env.example .env.dev
   ```

3. Open `.env.dev` and fill in the **4 required secrets**. The server will crash on startup if these are empty:
   - `LLM_PROVIDER="azure"` (or `"gemini"`)
   - `AZURE_OPENAI_API_KEY="your-key-here"` (or `GEMINI_API_KEY="..."`)
   - `MEMORY_OS_MONGODB_PASSWORD="admin123"` (matches `docker-compose.local.yml`)
   - `ARANGODB_PASSWORD="athena_dev"` (matches `docker-compose.local.yml`)

---

## 3. Start the Infrastructure

Start Redis, MongoDB, Milvus, ArangoDB, and MinIO in the background:

```bash
docker compose -f docker-compose.local.yml up -d
```

Wait 10–15 seconds for the databases to fully boot. ArangoDB and Milvus take the longest.

---

## 4. Initialize the Graph Schema

Before the server can run, the ArangoDB `athena_ltm` database and its collections must be scaffolded.

```bash
go run cmd/init-ltm/main.go
```

If you see connections refused, ArangoDB is still booting. Wait 10 seconds and try again.

---

## 5. Start the Server

Start the primary Athena API server:

```bash
go run cmd/memory-server/main.go
```

You should see log output indicating the server has connected to all 5 infrastructure dependencies and is listening on HTTP port 8080 and gRPC port 9090.

---

## 6. Smoke Test (The 3 Curls)

In a new terminal window, run these three curl commands to verify the system works end-to-end.

**(1) Create a session:**
```bash
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{"user_id": "test-user-123", "agent_id": "curl-test"}'
```
*Copy the `session_id` from the JSON response for the next steps.*

**(2) Store an interaction:**
```bash
curl -X POST http://localhost:8080/api/v1/sessions/<YOUR_SESSION_ID>/interactions \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{
    "user_message": "My name is John and I love writing Go code.",
    "agent_response": "Nice to meet you John, Go is a great language."
  }'
```

**(3) Retrieve context:**
```bash
curl -X GET http://localhost:8080/api/v1/sessions/<YOUR_SESSION_ID>/context \
  -H "X-API-Key: default-api-key"
```
*You should see your interaction returned in the `stm_events` array.*

---

## 7. Run Local Tests (Optional)

You can run the unit test suite without needing Docker running (it mocks the databases):

```bash
make test-short
```

If you want to run the full integration suite (which requires the Docker infrastructure to be running):

```bash
make test-e2e
```

*(Note: `test-e2e` actually loads `.env.dev` and will make external calls to your configured LLM API key).*

---

## Common Local Dev Errors

### `cannot find package "bitbucket.org/dromos/athena-memos/api/grpc/gen"`
**Cause:** The protobuf files haven't been generated yet.
**Fix:** Run `make generate`.

### `connection refused: dial tcp [::1]:8529...` during `init-ltm`
**Cause:** ArangoDB container hasn't finished its startup sequence.
**Fix:** Wait 15 seconds and re-run the command.

### `LLM circuit breaker tripped` locally
**Cause:** Your `AZURE_OPENAI_API_KEY` (or Gemini key) is invalid or hit a real rate limit.
**Fix:** Check your `.env.dev` credentials. The server will fast-fail requests for 60 seconds once the breaker trips.

### `panic: nil pointer dereference` in tests
**Cause:** Running `go test ./...` without Docker infrastructure running.
**Fix:** Run `make test-short` instead if Docker is down.

---

You are now ready to develop! Check `techdebt.md` for known issues or jump into `internal/memory/` where the core logic lives.
