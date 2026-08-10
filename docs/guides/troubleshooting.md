# Troubleshooting

Symptom-first. Each entry: what you see, why, what to do. The [Debug Tools](debug-tools.md) are the companion page.

## Startup

**Server exits immediately.**
The four required secrets are validated at boot: `LLM_PROVIDER`/`LLM_API_KEY` (or the Azure pair), `MEMORY_OS_MONGODB_PASSWORD`, `ARANGODB_PASSWORD`. The log names the missing one.

**`connection refused` to ArangoDB during `init-ltm`.**
ArangoDB boots slowly. Wait ten seconds, re-run; the command is idempotent.

**Server starts but `/health` says `degraded`.**
Redis or MongoDB is unreachable. The `/api/v1/health` endpoint reports per-dependency status with error strings.

## Nothing is being remembered

**Interactions store fine but no chains ever form.** In order of likelihood:

1. **Workers disabled**: check `ENABLE_MEMORY_WORKERS=true`.
2. **Not enough user messages**: chain-break checks need two user messages to compare; a single exchange forms nothing until more conversation arrives or the 20-event force flush hits.
3. **Same topic throughout**: no break, no chain (yet). The force flush (`STM_MAX_EVENTS_BEFORE_FLUSH`, default 20) eventually partitions long single-topic sessions.
4. **Quality gate rejection**: small talk is filtered by design. Check with `verifydb`; lower `MTM_QUALITY_MIN_SCORE` only if real content is being dropped.
5. **LLM failing**: run `test-gemini`; watch `llm_fallback_calls_total`.

**Chains form but nothing reaches the knowledge graph.**

1. Promotion threshold too high: `.env.example` ships `MEMORY_OS_MTM_HEAT_THRESHOLD=0.8` (conservative); the pipeline default is 0.3. Verify which your deployment uses.
2. Chains cooled before the promoter ran: check heat values in `verifydb` output against the [decay math](../concepts/heat-and-decay.md).
3. Extraction schema failures: check `memos_extractor_schema_failures_total` and server logs.
4. LTM disabled or ArangoDB unreachable: confirm `init-ltm` ran and `ARANGODB_*` vars are set.

## Configuration gotchas

**A setting seems ignored.**
Several knobs exist under two names read by different code paths. Set both:

| Pair |
|---|
| `STM_CACHE_MAX_TURNS` and `MEMORY_OS_STM_CACHE_MAX_TURNS` |
| `MEMORY_WORKER_COUNT` and `MEMORY_OS_NUM_WORKERS` |
| `REDIS_HOST` / `MONGO_URI` and their `MEMORY_OS_*` forms |

The [Configuration Reference](../reference/configuration.md) flags every alias pair.

**Heat weights don't add up.**
The scorer reads `HEAT_WEIGHT_INTRINSIC` (default 0.7) and `HEAT_WEIGHT_DENSITY` (default 0.3). The `HEAT_WEIGHT_COGNITIVE` variable that appears in `.env.example` is **not read by the code**; setting it does nothing. Use `HEAT_WEIGHT_DENSITY`, keep the pair summing to 1.0, and watch `memos_heat_score_distribution` after changing.

## Request errors

| HTTP | Meaning | Fix |
|---|---|---|
| 400 | Missing `user_id`, empty query, invalid event `type` | Fix the request |
| 401 | Auth enabled, credentials missing/wrong | See [Enabling Authentication](authentication.md) |
| 404 | Unknown `session_id` | Sessions are cheap; create a new one (memory is keyed by identity, [not session](../concepts/sessions-and-identity.md)) |
| 412 | Blob payload without a blob store | Configure `BLOB_*` ([guide](blob-storage.md)) |
| 500 | Datastore failure | Server logs name the store |

## Search returns nothing (or garbage)

- **Dimension mismatch after a provider switch**: the classic. Milvus collection was created for 1536-dim (Azure) and now receives 768-dim (Gemini) or vice versa. Recreate the collection ([procedure](llm-providers.md#the-dimension-trap)).
- **Recent conversation not searchable**: expected; it may still be in the STM window, unformed. Use `GetContext` for the window.
- **Old memory gone**: check for `status: "archived"` in `verifydb`; archived chains lose their vectors ([lifecycle](../concepts/archival-and-lifecycle.md)).

## Load and cost problems

**LLM bill or rate-limit errors spiking.**
Remember the multiplication: `MEMORY_WORKER_COUNT × replicas` versus `LLM_RATE_LIMIT_PER_MINUTE`. Retries are LIFO with no backoff, so a provider outage churns; the circuit breaker (default threshold 5, 60s open) is your protection. Also check whether a chatty automation is skipping [coalescing](storing-memory.md#coalescing-taming-automation-floods) by omitting `execution_id`/`step_id`.

**ArangoDB memory spikes at night.**
That's the analytics window. Pregel loads the whole graph; size the instance for it and never trigger analytics in-process ([the rule](running-graph-analytics.md#the-one-rule)).

## Inspecting a specific failed task

Task results persist for one hour:

```bash
redis-cli --scan --pattern 'task_results:v1:*' | head
redis-cli GET 'task_results:v1:<tenant>:<user>:<agent>:<taskId>'
```

The value carries `success`, an error message, and the processing timestamp.
