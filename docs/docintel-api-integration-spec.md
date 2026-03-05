# Athena Integration Spec: DocIntel-API

**Status:** Draft — For Review by DocIntel-API Team  
**Authored by:** Athena / Memory-OS Team

---

## 1. Overview

The `docintel-api` currently contains an embedded memory module (`internal/memory/`) that was an early prototype of what has since become Athena. **The entire embedded memory stack is being removed and replaced with calls to the standalone Athena MemOS service.** No refactoring — a full wholesale replacement.

This document does not dwell on the old code. It answers three questions:

1. **What does the integration look like?** — Which Athena API calls replace the embedded behavior, and how does the agent controller change?
2. **What do you delete?** — A definitive checklist.
3. **What do you add?** — New dependencies, env vars, and infrastructure configuration.

---

## 2. Conceptual Mapping

The embedded memory stack was modeled on a pipeline that is now fully operational inside Athena. The mapping is a straight replacement:

| What docintel-api does today (embedded) | What Athena does instead |
|-----------------------------------------|--------------------------|
| Redis STM cache (sliding window of turns) | `STMCache` in Athena, scoped by `tenant:user:agent` |
| Milvus vector search for past conversations | `GetContext(query=...)` triggers semantic MTM search |
| JanusGraph user persona facts | `GetContext` returns LTPM facts (active development) |
| Background workers: embedding creation, chain analysis | Fully managed by Athena's worker pool |
| Archivist: STM → MTM segment promotion | Athena's scheduled Archiver |
| Promoter: MTM → ArangoDB knowledge graph | Athena's scheduled Promoter |
| LLM-based topic continuity (cosine gate) | Athena's STM worker pipeline |

The `docintel-api` team writes zero worker code, zero embedding code, and zero Milvus/JanusGraph client code. All of that lives in Athena.

---

## 3. The Integration Contract: Three API Calls

The entire integration from `docintel-api`'s perspective reduces to three Athena calls:

### 3.1 At Conversation Start — `CreateSession`

Call this once per `conversationID` (when a new conversation is created in docintel). Store the returned `session_id` alongside the conversation document in MongoDB.

```go
session, err := athenaClient.CreateSession(ctx, &memoryos.CreateSessionRequest{
    TenantID: tenantID,          // from JWT
    UserID:   userID,            // from JWT
    AgentID:  "docintel-agent",
    Metadata: map[string]string{
        "conversation_id": conversationID,
        "origin_service":  "docintel",
    },
})
// Persist session.SessionID to the conversation document
```

**Identity requirements:**  
Athena enforces a firm identity hierarchy: `tenant_id → user_id → agent_id → session_id`. The `tenant_id` and `user_id` must come from the authenticated JWT token — never from client-supplied request fields. This is what gives cross-service intelligence its integrity: DocIntel conversations and Colabra conversations for the same user share the same `user_id`, so Athena's Promoter can merge their insights into a unified Long-Term Personal Memory graph.

---

### 3.2 Before Calling the AI Agent — `GetContext`

Replace the entire multi-step memory read pipeline in `AskQuestion` with a single call. Pass the user's message text as a `query` to activate semantic MTM search.

```go
// BEFORE (5 separate calls to Redis, Milvus, JanusGraph x2, MongoDB):
contextTurns, _    = stmCache.GetConversationContext(ctx, userID)
segments, _        = stmStore.RetrieveSegments(ctx, req.Text, 5)
kgFacts, _         = janusClient.GetFactsBySegments(ctx, segIDs)
pages, _           = stmStore.GetDialoguePagesByIDs(ctx, segment.PageIDs)
userPersona, _     = janusClient.GetFactsByUser(ctx, userID)

// AFTER (one call to Athena):
context, err := athenaClient.GetContext(ctx, sessionID, &memoryos.GetContextRequest{
    SessionId: sessionID,
    Query:     req.Text,   // triggers semantic MTM vector search
    Limit:     10,
})
// context.StmEvents      → recent conversation turns (replaces contextTurns)
// context.RelevantPages  → semantically similar past chains (replaces segments + pages)
// context.Ltpm           → user persona facts (replaces janusClient calls; in progress)
```

Inject `context.StmEvents` and `context.RelevantPages` into the LLM prompt as the provenance/context block, exactly as the current code assembles the `provenance` map.

**Session initialization vs. per-message:**  
If `Query` is empty (e.g., at the very start of a session before any user text), Athena returns MTM chains sorted by recency (`lastEventAt`). When `Query` is set, it performs vector similarity search. The same call handles both phases.

---

### 3.3 After the AI Agent Responds — `StoreInteraction`

Replace the async goroutine's STM cache write + task enqueue with a single Athena call. Keep only the `saveMessageToPersistentStorage` call for docintel's own conversation records.

```go
// BEFORE (goroutine with 3 separate operations):
go func() {
    stmCache.AddConversationTurn(ctx, userID, userTurn)
    stmCache.AddConversationTurn(ctx, userID, agentTurn)
    taskQueue.EnqueueMemoryTask(ctx, userID, req.Text, agentContent, metadata)
    saveMessageToPersistentStorage(ctx, ...)
}()

// AFTER (goroutine with 2 operations — Athena + docintel's own records):
go func() {
    if err := athenaClient.StoreInteraction(ctx, &memoryos.StoreInteractionRequest{
        SessionId:     sessionID,
        UserMessage:   req.Text,
        AgentResponse: agentContent,
    }); err != nil {
        log.Printf("WARN: Athena StoreInteraction failed: %v", err)
        // non-fatal — degrade gracefully
    }
    saveMessageToPersistentStorage(ctx, ...) // docintel's own record
}()
```

**What Athena handles end-to-end from this single call:**
- Writes both turns to the Redis STM cache, scoped to `tenant:user:agent`.
- Dual-writes both turns to MongoDB as `CognitiveEvent` documents.
- Enqueues a `CognitiveChainCheckTask` to the scoped worker queue.
- Worker runs cosine similarity gate; creates or continues a `CognitiveChain`.
- Archiver (scheduled) promotes cold chains to MTM `Segment` summaries + Milvus embeddings.
- Promoter (scheduled) promotes high-heat segments to ArangoDB knowledge graph triples.

---

### 3.4 The `AskQuestionAboutBrief` Endpoint

This endpoint performs an additional semantic memory search (to find relevant past dialogue pages). Replace `stmStore.RetrieveFromSTMStore(ctx, userID, searchQuery, 5)` with `athenaClient.SearchMemory`:

```go
results, err := athenaClient.SearchMemory(ctx, &memoryos.SearchMemoryRequest{
    SessionId: sessionID,
    Query:     searchQuery, // brief title + user question
    Limit:     5,
})
// results.Results → equivalent of semanticMemories (DialoguePages)
```

---

## 4. Connecting the Athena Client

### 4.1 Client Initialization

Add to `cmd/api/main.go` (or `InitAPIClient` in `controllers/agent.go`):

```go
import "bitbucket.org/dromos/memory-os/pkg/memoryos"

athenaClient = memoryos.NewClient(memoryos.ClientConfig{
    BaseURL: os.Getenv("ATHENA_BASE_URL"),
    APIKey:  os.Getenv("ATHENA_API_KEY"),
    Timeout: 30 * time.Second,
})
```

Wire the `athenaClient` as a package-level global alongside (and eventually replacing) `stmCache`, `taskQueue`, and `stmStore`.

### 4.2 Session ID Storage

Add an `AthenaSessionID string` field to the conversation MongoDB model (if not adopting Athena's UUID as the canonical ID):

```go
type Conversation struct {
    ID              primitive.ObjectID `bson:"_id,omitempty"`
    UserID          string             `bson:"userId"`
    AthenaSessionID string             `bson:"athenaSessionId,omitempty"` // NEW
    // ... existing fields
}
```

Resolve the `AthenaSessionID` from the conversation document at the start of each agent request handler (once it's stored at conversation creation time, this is just a field read from the existing MongoDB fetch).

---

## 5. New Environment Variables

Add to `.env.example`:

```bash
# Athena Memory OS
ATHENA_BASE_URL=http://memory-os:8080
ATHENA_API_KEY=your-service-level-api-key
```

---

## 6. What to Delete

Once the Athena integration is wired and validated, delete the following entirely:

**Directories / packages:**
```
internal/memory/        ← entire directory (25 files)
internal/kg/janus/      ← entire directory (after LTPM gap is closed)
```

**Startup code in `cmd/api/main.go`:**
- Remove the `ENABLE_MEMORY_WORKERS` / `MEMORY_WORKER_COUNT` block.
- Remove `cache.NewRedisClient()` for memory workers.
- Remove `database.ConnectMemoryMongoDB()` (the secondary DB connection).

**Globals in `controllers/agent.go`:**
- Remove `stmCache`, `taskQueue`, `stmStore`, `janusClient` declarations.
- Remove `InitAPIClient`'s Redis + STM initialization block.
- Remove `janusClient.BootstrapSchema()` + `SeedAgent()` startup goroutine.

**Environment variables to retire:**
```
# Remove from .env and .env.example:
MILVUS_HOST, MILVUS_PORT, MILVUS_DATABASE
JANUS_ENDPOINT
GOOGLE_PROJECT_ID, GOOGLE_LOCATION, EMBEDDING_MODEL_NAME, EMBEDDING_DIMENSIONS
STM_CACHE_TTL, STM_CACHE_MAX_TURNS, STM_CACHE_KEY_PREFIX
CHAIN_SIM_HIGH, CHAIN_SIM_LOW
LLM_RATE_LIMIT_PER_MINUTE, LLM_CIRCUIT_BREAKER_THRESHOLD, LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS
ENABLE_MEMORY_WORKERS, MEMORY_WORKER_COUNT
```

**Infrastructure to decommission (docintel-specific):**

| Service | Status |
|---------|--------|
| Milvus instance | Decommission — now owned entirely by Athena |
| JanusGraph instance | Decommission — Athena uses ArangoDB |
| Redis (memory workers) | Decommission if only used for STM/task queue |
| Google Vertex AI credentials | Decommission — Athena handles embeddings internally |

---

## 7. Phased Migration

| Phase | What Ships | Risk |
|-------|-----------|------|
| **1** | Add `AthenaClient`, `CreateSession` on conversation creation | Low — additive |
| **2** | Replace async write goroutine with `StoreInteraction` | Medium — core write path |
| **3** | Replace 5-step read pipeline with `GetContext`, validate context quality | Medium — core read path |
| **4** | Delete `internal/memory/`, retire infra | Low once phases 1–3 validated |
| **5** | Delete `internal/kg/janus/` once LTPM is live in `GetContext` | Conditional |

---

## 8. Open Questions for the Athena Team

1. **LTPM in `GetContext`:** `context.Ltpm` currently returns `{status: "not_implemented"}`. When will user persona facts (equivalent to `janusClient.GetFactsByUser`) be populated? Phase 5 depends on this.

2. **Session vs. Conversation:** Should one Athena `session_id` map to one `conversationID` (a single conversation thread), or to a user's entire session across multiple conversations? The Athena team's recommendation on the intended scope of a `session` will determine how often `CreateSession` is called.

3. **Tenant ID:** Should `docintel-api` use a static platform-level `tenant_id` (e.g., `"dromos"`) or pass through a per-organization ID from the JWT? This affects knowledge graph isolation between customers.

4. **LPM Personality Profiles:** The `internal/memory/lmp_*.go` files implement a 90-dimension user personality analysis system that exists nowhere else. Is this functionality being ported to Athena, deprecated, or extracted into its own service?

---

## 9. Requirements Summary for the DocIntel-API Team

A consolidated checklist of every action required from the `docintel-api` team to complete this integration.

### Code Changes

| # | Requirement | Location | Phase |
|---|-------------|----------|-------|
| R1 | Add `bitbucket.org/dromos/memory-os/pkg/memoryos` as a Go module dependency | `go.mod` | 1 |
| R2 | Initialise `AthenaClient` at startup with `ATHENA_BASE_URL` and `ATHENA_API_KEY` | `controllers/agent.go` → `InitAPIClient()` | 1 |
| R3 | Add `AthenaSessionID string` field to the Conversation MongoDB model | conversation model struct | 1 |
| R4 | Call `athenaClient.CreateSession(...)` when a new conversation is created; persist the returned `session_id` on the conversation document | conversation creation handler | 1 |
| R5 | Replace async write goroutine (`AddConversationTurn` + `EnqueueMemoryTask`) with `athenaClient.StoreInteraction(...)` | `controllers/agent.go` → `AskQuestion`, `AskQuestionAboutBrief` | 2 |
| R6 | Replace the 5-call memory read pipeline with `athenaClient.GetContext(sessionID, query, limit)` | `controllers/agent.go` → `AskQuestion` | 3 |
| R7 | Replace `stmStore.RetrieveFromSTMStore(...)` with `athenaClient.SearchMemory(...)` | `controllers/agent.go` → `AskQuestionAboutBrief` | 3 |
| R8 | Remove `stmCache`, `taskQueue`, `stmStore`, `janusClient` package-level globals | `controllers/agent.go` | 4 |
| R9 | Remove memory worker startup block (`ENABLE_MEMORY_WORKERS`, `MEMORY_WORKER_COUNT`) | `cmd/api/main.go` | 4 |
| R10 | Remove secondary MongoDB connection (`ConnectMemoryMongoDB`) | `cmd/api/main.go` | 4 |
| R11 | Delete `internal/memory/` entirely (25 files) | directory | 4 |
| R12 | Delete `internal/kg/janus/` (after LTPM gap is confirmed closed) | directory | 5 |

### Configuration Changes

| # | Requirement | Action |
|---|-------------|--------|
| C1 | Add `ATHENA_BASE_URL` to `.env` and `.env.example` | Add |
| C2 | Add `ATHENA_API_KEY` to `.env` and `.env.example` | Add |
| C3 | Remove `MILVUS_HOST`, `MILVUS_PORT`, `MILVUS_DATABASE` | Delete |
| C4 | Remove `JANUS_ENDPOINT` | Delete |
| C5 | Remove `GOOGLE_PROJECT_ID`, `GOOGLE_LOCATION`, `EMBEDDING_MODEL_NAME`, `EMBEDDING_DIMENSIONS` | Delete |
| C6 | Remove `STM_CACHE_TTL`, `STM_CACHE_MAX_TURNS`, `STM_CACHE_KEY_PREFIX` | Delete |
| C7 | Remove `CHAIN_SIM_HIGH`, `CHAIN_SIM_LOW` | Delete |
| C8 | Remove `LLM_RATE_LIMIT_PER_MINUTE`, `LLM_CIRCUIT_BREAKER_THRESHOLD`, `LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS` | Delete |
| C9 | Remove `ENABLE_MEMORY_WORKERS`, `MEMORY_WORKER_COUNT` | Delete |

### Infrastructure Changes

| # | Requirement | Who Owns |
|---|-------------|----------|
| I1 | Ensure `docintel-api` can reach `memory-os` over the internal network (Docker / K8s) | Platform / DevOps |
| I2 | Provision a `docintel-api` service-level API key in Athena | Athena Team |
| I3 | Agree on `tenant_id` value (static platform ID or per-org JWT claim) | Athena + DocIntel Teams |
| I4 | Decommission Milvus instance (docintel-specific) | Platform / DevOps |
| I5 | Decommission JanusGraph instance (docintel-specific) | Platform / DevOps |
| I6 | Remove Vertex AI credentials from docintel-api's service account | Platform / DevOps |
| I7 | Redis: confirm whether Redis is used for anything else in docintel-api; decommission if memory was its only use | DocIntel Team |

### Blockers / Dependencies on the Athena Team

| # | Blocker | Blocks Phase |
|---|---------|-------------|
| B1 | `ATHENA_API_KEY` provisioned for docintel-api | 1 |
| B2 | Agreement on `tenant_id` convention | 1 |
| B3 | Agreement on `session` scope (per-conversation vs. per-user-session) | 1 |
| B4 | `GetContext` LTPM field populated (user persona facts) | 5 |
| B5 | Decision on 90-dimension personality profiles (`lmp_*.go`) — port, deprecate, or extract | Before Phase 4 deletion |

---

## 10. DocIntel Agent Integration with Athena Memory OS

> **Who is this section for?**
> This section is for engineers working on the three DocIntel autonomous agents — `docintel-scout-agent`, `docintel-guided-scout-agent`, and `docintel-analyst-agent`. It explains precisely how each agent relates to Athena Memory OS, what they write to it, what they can optionally read from it, and exactly how to build the Python Tools to do so. The goal is that this section answers every question without needing to escalate to the Athena team.

---

### 10.1 The Two Graph Systems: A Critical Distinction

The DocIntel system and Athena Memory OS each maintain their own separate ArangoDB database. **These are not the same graph and they serve fundamentally different purposes.**

| | Odin Knowledge Graph (DocIntel) | Athena Memos Graph |
|---|---|---|
| **Database** | A dedicated ArangoDB instance managed by the `odin-kg-engine` | Athena's own ArangoDB instance, managed entirely by Athena's internals |
| **Contents** | `TextBlocks`, `ExtractedEntities`, `documents`, `communities`, and `edges` derived from ingested business documents | `Identities`, `Concepts`, `Projects`, and `MemoryEdges` derived from human-AI conversations |
| **Purpose** | Store and make navigable the knowledge extracted from a corpus of documents — reports, PDFs, filings — to support statistical analysis and pattern recognition | Store and make retrievable the Long-Term Personal Memory (LTPM) of a user across any AI platform — what they know, care about, and have discussed |
| **Who writes to it** | The DocIntel ingestion pipeline | Athena's own STM → MTM → Promoter pipeline, and (via Section 10.4) the DocIntel Analyst agent |
| **Who reads from it** | The DocIntel Scout agents via the 16 Odin engine tools | Athena's `GetContext` API; optionally, the DocIntel Guided Scout agent |

**Key insight:** When a Scout agent calls `vector_search` or `inspect_node`, it is querying the **Odin graph** (document intelligence). It is not talking to Athena at all. Those tools connect to the DocIntel ArangoDB instance via the `odin_kg_engine` wheel. When the Analyst agent calls `publish_verified_hunch`, it is writing to the **Athena Memos graph** (personal memory). These are separate, complementary systems.

---

### 10.2 Agent-by-Agent Integration Matrix

| Agent | Reads from Athena? | Writes to Athena? | Notes |
|---|---|---|---|
| **`docintel-scout-agent`** | ❌ No | ❌ No | Fully autonomous with no user scope. Explores the Odin document graph. Athena's personal memory graph is irrelevant to its mission. |
| **`docintel-guided-scout-agent`** | 🔶 Optional | ❌ No | Receives a `user_goal` string from the user. Can optionally query Athena's LTM to add the user's past expressed interests as seed context — useful when the goal is vague. The goal string itself is sufficient in most cases. See Section 10.5. |
| **`docintel-analyst-agent`** | ❌ No | ❌ No | The Analyst mathematically verifies hunches and saves its final findings back into the Odin Knowledge Graph or DocIntel's MongoDB. It does **not** write to Athena. See Section 10.3 for the federated architecture design. |

---

### 10.3 The Federated Architecture: Why the Analyst Does Not Write to Athena

A common misconception is that the Analyst agent should write its verified business findings (`"Q3 Churn is 5.4%"`, `"Enterprise clients require SOC2"`) directly into Athena's Long-Term Memory (LTM). **This is an anti-pattern and must be avoided.**

Athena's schema (`Identities`, `Concepts`, `Projects`) is an **operational personal memory graph** designed to capture what a user cares about and knows. Odin is a **Business Intelligence graph** designed to capture statistical facts, temporal anchors, and document citations.

Mixing these two graphs creates severe problems:
1. **Retrieval Pollution:** If a user asks a simple conversational query, a vector search might accidentally pull in 50 rows of irrelevant business metrics from last year's Analyst runs, blowing out the LLM context window.
2. **Schema Friction:** Athena's edge relationships (`RELATES_TO`, `IMPLIES`) cannot natively store the rich metadata (P-values, standard deviations, document spans) that the Analyst generates.

#### The Federated Solution

Instead of the Analyst writing *into* Athena, we use a federated tool approach:

**1. What Odin/DocIntel knows:**
The Analyst writes its verified hunches and PDF reports back into the DocIntel infrastructure (Odin Graph or MongoDB). It stays there.

**2. What Athena knows:**
Athena's graph only tracks that the analysis *event* occurred.
* *Node:* `Mission: Churn Analysis`
* *Node:* `User: Sangalo`
* *Edge:* `User -> INITIATED -> Mission: Churn Analysis`

**3. The Tool Bridge:**
When the user asks Athena, *"What did we find out about the churn rate last week?"*, Athena's LLM realizes it needs business data. Athena invokes a Tool (e.g., `query_docintel_findings`) to ask the DocIntel API for the answer in real-time. Athena reads the findings, answers the user, and discards the business data from its own memory context.

**Conclusion:** The DocIntel Analyst agent requires zero integration with the Athena Memos Graph. It should store its findings in DocIntel.

---

### 10.4 [Intentionally Blank]

*This section previously detailed a direct LTM write path for the Analyst agent via a `publish_verified_hunch` tool. This approach has been deprecated in favor of the federated architecture described in Section 10.3.*

---

### 10.5 Optional: Guided Scout Enrichment via `recall_persona_context`

This is **optional** enrichment. When the user's goal is already descriptive (`"Analyse customer lifetime value for enterprise accounts"`), the Guided Scout has sufficient signal from `vector_search` and `search_entities` probes. This call is only worth making when feasibility is unclear because the goal is vague (`"Look into revenue"`).

**When to call it:** After `_decompose_goal()` but before `_probe_kg()`, only if the goal string contains fewer than 5 meaningful words or lacks domain-specific vocabulary. The results should be injected into the `_generate_verdict()` prompt to improve seed entity selection.

```python
# In guided_scout_agent/tools/athena.py

async def recall_persona_context(
    session_id: str,
    user_goal: str,
) -> dict[str, Any]:
    """
    Query Athena's GetContext to retrieve LTM memory nodes relevant to the
    user's goal. Use sparingly — only when the user_goal is vague and you need
    persona context to improve seed entity selection.

    The session_id must be created by the Guided Scout's HTTP handler at request
    start via Athena's CreateSession API, before the OODA loop is launched. The
    handler must pass the session_id into the mission state.

    Args:
        session_id: The Athena session ID for the active user session.
        user_goal: The user's free-form goal string. Used as the semantic search
                   query to find relevant past LTM nodes.

    Returns:
        dict with 'ltpm.nodes' (list) and 'ltpm.edges' (list). Inject these
        into the mission planning prompt as user context / prior knowledge.
    """
    async with httpx.AsyncClient(timeout=30.0) as client:
        response = await client.get(
            f"{ATHENA_BASE_URL}/v1/memory/context",
            params={"session_id": session_id, "query": user_goal, "limit": 10},
            headers={"Authorization": f"Bearer {ATHENA_API_KEY}"},
        )
        response.raise_for_status()
        data = response.json()

    # 'ltpm.nodes' will contain Concept/Identity nodes the user has discussed
    # before that are semantically related to the goal — useful for seeding
    # the mission with entities the user is already known to care about.
    return data.get("ltpm", {})
```

**Session ID plumbing:**
The `session_id` must be created by the Guided Scout's FastAPI handler at the start of the `POST /scout` request:
```python
# In guided_scout_agent/server.py, inside the /scout handler:
session = await athena_client.create_session(tenant_id, user_id, "docintel-guided-scout")
mission_state.athena_session_id = session["session_id"]
```
Pass `mission_state.athena_session_id` into the tool call. **Do not hardcode a session ID.**

---

### 10.6 Environment Variables for the DocIntel Agents

Add to each agent's `.env` / `docker-compose.yml`. The Scout and Analyst agents require none of these since they do not interact with Athena.

```bash
# ── Athena Memory OS ──────────────────────────────────────────────────────────
# Required by: docintel-guided-scout-agent (Section 10.5 optional only)
# Not needed by: docintel-scout-agent, docintel-analyst-agent
ATHENA_BASE_URL=http://memory-os:8080   # Internal K8s/Docker service name for Athena
ATHENA_API_KEY=<service-api-key>        # Provisioned by the Athena team (Blocker A1)
```

No other Athena configuration is needed.

---

### 10.7 Blockers Specific to Agent Integration

| # | Blocker | Blocks Which Agent | Who Resolves It |
|---|---------|-------------------|-----------------|
| A1 | `ATHENA_API_KEY` provisioned for `docintel-guided-scout` service | Guided Scout | **Athena team** — a dedicated service-level API key must be issued. |
| A2 | Agreement on `tenant_id` value | Guided Scout | **Both teams** — static platform ID (`"dromos"`) or a per-organisation claim from the request JWT. |
| A3 | Session ID plumbing in the Guided Scout HTTP handler | Guided Scout (Section 10.5 optional) | **DocIntel team** — the `/scout` handler must call `CreateSession` and pass the returned `session_id` into the mission state before launching the OODA loop. |

