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

The test step runs `go test ./pkg/memoryos ./api/middleware`. Both packages currently have **no test files**, so CI tests pass vacuously. The real test suite lives in `internal/memory/` and `internal/database/`, but these require Docker infrastructure and cannot run in the base `golang:1.24` CI image without service containers.

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
