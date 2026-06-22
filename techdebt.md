# Technical Debt & Known Gaps

_Last updated: March 11, 2026_

---

## RESOLVED ✅

### EMA Confidence Formula (PR #7 — reverted March 10, 2026)
An exponential moving average (`OLD + (NEW - OLD) * 0.4`) was introduced for `MemoryEdges.confidence` and a dead `context_history` field was added to the AQL UPSERT. The EMA caused asymptotic decay toward 0 for low-confidence edges, eventually pushing them below the hard retrieval threshold (`FILTER e.confidence >= 0.5` in `ltm_reader.go`), silently removing them from all agent context queries. Reverted to simple average `(OLD + NEW) / 2`. `context_history` removed — it was never read by any code path.

**Residual:** Edges written between ~March 9 and March 10 may have EMA-skewed confidence values and orphaned `context_history` arrays. A one-time AQL migration is needed to audit edges near the 0.5 threshold and strip the dead field. No migration has been run yet.

### In-Process Pregel Scheduler (PR #7 — reverted March 10, 2026)
A 6-hour in-process goroutine was introduced to call `promoter.TriggerAnalytics` on a ticker. This violated the ArangoDB infra spec (Pregel must not run in-process due to full graph load into memory — OOM risk on large tenants). The goroutine was removed. Community detection must be triggered via the K8s CronJob at 03:00 UTC daily (`POST /api/v1/admin/analytics/trigger`).

### Generated Protobuf Files in Git (fixed March 10, 2026)
`api/grpc/gen/*.pb.go` and `*.pb.gw.go` were accidentally committed. Removed from tracking, added `*.pb.go` and `*.pb.gw.go` to `.gitignore`. CI now generates them fresh via the `Generate Protobuf` pipeline step. Do not re-add these files to git.

---

## OPEN ⚠️

### 1. Missing Unit Tests for `server.go`

**Severity:** High  
**File:** `internal/server/server.go`

No tests exist for the gRPC service implementation. Critical logic that is not covered:
- Smart Trigger: tasks enqueued only for `role=user, type=message` (not for agent/system events or non-message types)
- `StoreInteraction` dual-write correctness (Redis + MongoDB)
- `StoreEvent` Claim Check routing (blob payloads offloaded correctly)
- `GetContext` response structure (STM + MTM merge)

**Action:** Mock the gRPC context and underlying stores. Assert `AddSTMEvent` and `EnqueueTask` are called under exactly the correct conditions. Then add the package to the pipeline test step.

---

### 2. `GetContext` Does Not Return LTM Data

**Severity:** Medium  
**File:** `internal/server/server.go` (GetContext handler)

The `LTPMContext` field in the `GetContext` response is hardcoded to `status: "not_implemented"`. The LTM read path (`LTMReader.FetchContext`, `LTMReader.SearchByQuery`) is fully implemented and is used by `SearchMemory`, but it is not wired into `GetContext`. Agents that call `GetContext` do not receive any LTM knowledge graph enrichment.

**Action:** After retrieving STM events and MTM chains in `GetContext`, call `LTMReader.FetchContext` with the session's user/tenant IDs and a set of seed nodes derived from the MTM chain entities. Populate `LTPMContext` with the graph result.

---

### 3. Pipeline CI Tests Still Cover Only Empty Packages

**Severity:** Medium  
**File:** `bitbucket-pipelines.yml`

The test step runs `go test ./pkg/memoryos ./api/middleware`. Both packages currently have **no test files**, so CI tests pass vacuously. The real test suite lives in `internal/memory/` and `internal/database/`, but these require Docker infrastructure and cannot run in the base `golang:1.26` CI image without service containers.

**Action (Option A — Short-term):** Add Bitbucket `services:` blocks for Redis, MongoDB, Milvus, and ArangoDB to the test step and run `go test -short ./internal/...`. The `-short` flag skips integration tests that make external LLM calls.

**Action (Option B — Long-term):** Separate unit tests from integration tests via build tags. Tag integration tests with `//go:build integration` and run only `go test ./...` (no tag) in CI.

---

### 4. Worker Retry is LIFO with No Backoff

**Severity:** Medium  
**File:** `internal/memory/worker.go` — `reEnqueueTask()`

When a worker task fails (e.g., LLM API down), the task is re-enqueued via `LPUSH` back to the head of the scoped queue. This means:
- The same task will be retried immediately on the next BRPop cycle.
- During sustained LLM outages, the worker will spin in a tight loop, consuming Redis bandwidth and wasting LLM quota on retries that are doomed to fail.
- `maxRetries=3` is enforced, but all three retries can happen within milliseconds.

**Action:** Add exponential backoff using a `ZADD`-based delay queue (scored by `time.Now().Add(delay).Unix()`), a separate goroutine to move ready items back to the work queue, and a dead-letter list for tasks that have exhausted retries.

---

### 5. `STM_CACHE_MAX_TURNS` vs `MEMORY_OS_STM_CACHE_MAX_TURNS` Naming Inconsistency

**Severity:** Low  
**Files:** `internal/config/config.go`, `internal/memory/stm_cache.go`

`config.go` reads `MEMORY_OS_STM_CACHE_MAX_TURNS`; `stm_cache.go` reads the unprefixed `STM_CACHE_MAX_TURNS`. These are logically the same setting controlling the STM sliding window size, but they are read from different env vars. Setting `STM_CACHE_MAX_TURNS` in production silently ignores the prefixed form in config.go and vice versa.

**Action:** Consolidate to the prefixed form `MEMORY_OS_STM_CACHE_MAX_TURNS` everywhere. Remove the unprefixed read from `stm_cache.go`.

---

### 6. Promoter Interval Default Mismatch (README vs Code)

**Severity:** Low  
**Files:** `cmd/memory-server/main.go`, `README.md`

The README previously stated the default promoter interval was 5 minutes. `main.go` defaults `MEMORY_OS_PROMOTER_INTERVAL_MIN` to `30` minutes. README has been corrected. Verify deployed environment variables match the intended cadence.

---

### 7. AQL Data Pollution from PR #7 (Production)

**Severity:** Medium  
**Database:** ArangoDB `athena_ltm`, collection `MemoryEdges`

Edges written between ~March 9 20:40 UTC and March 10 (when the EMA revert was deployed) have:
1. **EMA-skewed confidence values** — potentially lower than they should be if the edge was reinforced multiple times. Edges close to 0.5 may now appear below the retrieval threshold.
2. **`context_history` arrays** — a dead field consuming storage on every affected edge document.

**Action (one-time AQL migration, requires DBA review):**
```aql
-- Strip context_history from all edges
FOR e IN MemoryEdges
  FILTER HAS(e, 'context_history')
  UPDATE e WITH { context_history: null } IN MemoryEdges OPTIONS { keepNull: false }

-- Audit edges near the confidence threshold
FOR e IN MemoryEdges
  FILTER e.confidence >= 0.3 AND e.confidence < 0.5
  RETURN { _key: e._key, from: e._from, to: e._to, confidence: e.confidence, weight: e.weight }
```
Review the audit results and determine if any edges should be manually boosted back above 0.5. No migration has been run as of this writing.

---

### 8. Broken Test Fixtures

**Severity:** Low  
**Files:**
- `internal/memory/stm_store_azure_integration_test.go` — asserts embedding model is `text-embedding` family and dimensions are `3072`. Actual deployment uses `gemini-embedding-001` at `1536` dims.
- `internal/memory/mtm_parallel_processor_test.go` — LLM mock not intercepting calls; tests fall back to heuristic and assert LLM outputs that never arrive.
- `internal/server/server_test.go` — `TestStoreInteraction_SmartTrigger` panics with nil pointer; `STMStore` in test setup has no MongoDB client.

These failures pre-date PR #7 and indicate tests were written to match a former config or were never fully wired. They do not indicate production bugs but inflate noise in test runs.

**Action:** Update `stm_store_azure_integration_test.go` to match current embedding config. Fix mock interceptor wiring in `mtm_parallel_processor_test.go`. Add MongoDB mock (e.g., `mongotest`) to `server_test.go` to prevent nil pointer.

---

### 8. LTM Belief Revision — No Contradiction Detection or Fact Retraction

**Severity:** Medium (silent correctness risk over long-lived graphs)
**Files:** `internal/memory/ltm_writer.go`, `internal/memory/graph_extractor.go`, `pkg/memory/analytics.go`

The LTM write path is purely additive. The UPSERT logic can only do two things: insert a new edge or reinforce an existing one. There is no mechanism for:

- **Contradiction detection** — the system cannot recognize that a newly promoted episode ("the user no longer uses Go") semantically negates an existing, heavily reinforced edge (`user → USES → go`).
- **Edge retraction** — there is no operation to remove or negate a triple. The graph only grows.
- **Temporal scoping** — edges carry a `last_seen` timestamp but have no validity window. A fact that was true two years ago is treated identically to a fact observed yesterday.
- **Confidence decay on edges** — unlike Cognitive Chains, which decay via the Ebbinghaus curve, edges in LTM have no time-based decay. A stale edge with high weight from past reinforcement will continue to rank highly in traversals indefinitely.

**Why it matters:** A user preference, tool choice, or project assignment that changes over time will accumulate contradictory edges. The old edge will not be demoted; it will coexist alongside the new one. Over a long-lived graph, this produces a retrieval layer that mixes current facts with outdated ones without any indication of which is more recent or more reliable.

**Current partial mitigation:** None. The confidence averaging (`(OLD + NEW) / 2`) would marginally dilute a strong edge if a new extraction assigned a low confidence to the same relationship — but a contradictory episode typically produces a *different* triple (e.g., `user → EXPRESSED_INTEREST → plant_based_diet`), not a low-confidence re-assertion of the old one. The two coexist rather than one superseding the other.

**Approaches to address:**
1. **Temporal edge semantics** — add a validity window to edges (`valid_from`, `valid_until`) and emit a retraction triple when contradictions are detected at extraction time.
2. **Contradiction detection at extraction** — the graph extractor receives the current entity neighborhood alongside the episode summary and is prompted to identify retractions explicitly, not just assertions.
3. **Edge decay** — extend the Ebbinghaus heat model to edges, so relationships that are never reinforced across new promotions gradually lose retrieval ranking even if their absolute weight is high.

**Note:** This gap is also documented as a research limitation in the paper (§2.5) pending resolution before publication.

---

## RESEARCH DEBT 📋

*Questions that must be answered in §6 (Empirical Evaluation) before the paper can be submitted. These are not engineering bugs — they are empirical claims made implicitly by the architecture description that require experimental substantiation.*

---

### R1. Dual-Threshold Calibration — Chain-Break Detection

**Raised in:** §2.6 (The Consolidation Arc)
**Blocking:** §6

The architecture describes a dual-threshold approach to topic boundary detection using cosine similarity, with a gray zone handled by LLM arbitration. The current implementation uses specific threshold values that were set by engineering judgment. The paper must provide empirical justification before these can be stated as design choices rather than arbitrary configuration.

**Questions §6 must answer:**
1. How were the high and low thresholds selected? Were they tuned on a labeled dataset of topic transitions, or derived analytically?
2. What is the overall accuracy of the chain-break detector (precision, recall, F1) against a ground-truth set of manually labeled topic boundaries?
3. What percentage of cases are resolved by cosine similarity alone vs. requiring LLM arbitration? What does this distribution look like across different interaction types (dialogue, agentic workflows, mixed)?
4. What is the sensitivity of the system to threshold choice — how much does performance degrade if the thresholds are shifted ±0.05?

---

### R2. LLM Model Sensitivity — Gray-Zone Binary Classifier

**Raised in:** §2.6 (The Consolidation Arc)
**Blocking:** §6

The gray-zone classifier sends both messages to an LLM with a binary classification prompt. The paper asserts (by design rationale) that this task is well-suited to a small, low-latency model. This claim requires empirical support.

**Questions §6 must answer:**
1. Was the binary classifier evaluated across different model sizes (e.g., small instruction-tuned model vs. mid-size vs. large reasoning model)?
2. Is there a measurable accuracy difference between model sizes on this specific task?
3. What is the latency cost of LLM arbitration per gray-zone call, and how does it vary by model size?
4. What is the token cost per arbitration call, and what does that imply for production operating cost at scale?

---

### R3. Cosine Similarity Accuracy — Embedding Model Choice

**Raised in:** §2.6 (The Consolidation Arc)
**Blocking:** §6

The chain-break detection relies on embedding vectors for semantic similarity. The quality of these embeddings directly determines the reliability of the high-confidence regions of the dual-threshold algorithm.

**Questions §6 must answer:**
1. Which embedding model is used? Was the choice evaluated against alternatives?
2. What is the false positive rate (same-topic pairs classified as breaks) and false negative rate (topic shifts missed entirely) at the chosen threshold pair?
3. Are there interaction types (e.g., highly technical agentic workflows vs. conversational dialogue) where cosine similarity performs significantly better or worse?

