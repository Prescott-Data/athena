# Your First Session

Store a memory, read it back, and watch the cognitive pipeline process it in the background. Requires a running stack from [Deploy in Five Minutes](quickstart.md).

## 1. Create a session

Every memory operation happens inside a session scoped to a `tenant → user → agent` hierarchy:

```bash
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{"user_id": "test-user-123", "agent_id": "curl-test"}'
```

```json
{"session_id": "a1b2c3d4-...", "created_at": "2026-08-09T16:00:00Z"}
```

Export it for the next steps:

```bash
export SESSION=a1b2c3d4-...
```

!!! note
    With auth disabled (the local default) the `X-API-Key` header is ignored but harmless. When JWT auth is enabled, `tenant_id` and `user_id` come from the token, never from the request body. See [Security Model](../concepts/security-model.md).

## 2. Store an interaction

An *interaction* is one user↔agent turn. Athena writes both events to the STM window and queues a cognitive-chain check:

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/interactions \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{
    "user_message": "My name is John and I love writing Go code.",
    "agent_response": "Nice to meet you John, Go is a great language."
  }'
```

```json
{"success": true}
```

## 3. Read the context back

```bash
curl "http://localhost:8080/api/v1/sessions/$SESSION/context?limit=10" \
  -H "X-API-Key: default-api-key"
```

Your turn appears in `stm_events`, newest first. This is the short-term window an agent would prepend to its prompt. Add `&query=...` to blend in semantically relevant mid-term memories; see [Retrieving Context](../guides/retrieving-context.md).

## 4. Watch the pipeline work

Store a few more interactions on **different topics** (chains break on topic shifts; see [The Cognitive Pipeline](../concepts/cognitive-pipeline.md)), then look behind the curtain:

**Cognitive chains in MongoDB**: the worker distills topic segments into summarized chains:

```bash
go run cmd/verifydb/main.go        # dumps cognitive_chains
```

**Pipeline metrics**: every stage is instrumented:

```bash
curl -s http://localhost:8080/metrics | grep -E "memos_|stm_cache|cosine"
```

**Semantic search**: once a chain has formed, search it from another conversation:

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/context/search \
  -H "Content-Type: application/json" \
  -H "X-API-Key: default-api-key" \
  -d '{"query": "what programming language does the user like?", "limit": 5}'
```

**The knowledge graph**: after the promoter has run (30-minute ticker by default), inspect what got promoted to ArangoDB at [http://localhost:8529](http://localhost:8529) (database `athena_ltm`), or:

```bash
go run cmd/verify_analytics/main.go
```

## What just happened

1. Your interaction was **dual-written** to Redis (hot window) and MongoDB (durable log).
2. A `cognitive_chain_check` task was queued; a worker compared your message's embedding against the previous one to detect topic breaks.
3. On a break (or 20-event overflow), the segment was summarized into a **cognitive chain** with topic, entities, and an embedding in Milvus.
4. The promoter scored the chain's **heat**; anything ≥ 0.3 gets its knowledge extracted into the **LTM graph**.

Next: the full mental model in [Architecture](../concepts/architecture.md), or go straight to [Storing Memory](../guides/storing-memory.md) for events, roles, and blob payloads.
