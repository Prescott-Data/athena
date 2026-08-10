# Workers & Schedulers

The background machinery has a handful of knobs. This guide covers what each controls and how to tune them; the algorithms themselves are in [The Cognitive Pipeline](../concepts/cognitive-pipeline.md), [Heat & Decay](../concepts/heat-and-decay.md), and [Archival & Lifecycle](../concepts/archival-and-lifecycle.md).

## The switches

| Variable | Default | Controls |
|---|---|---|
| `ENABLE_MEMORY_WORKERS` | `true` | Master switch. Off = plain STM store: no chains, no promotion, ever |
| `MEMORY_WORKER_COUNT` | `2` | Worker goroutines per server replica |
| `MEMORY_OS_PROMOTER_INTERVAL_MIN` | `30` | Promoter ticker (minutes) |
| `MEMORY_OS_ARCHIVER_INTERVAL_MIN` | `60` | Archiver ticker (minutes) |
| `STM_TASK_TIMEOUT` | `300` | Per-task budget in seconds (monitoring, not enforcement) |

For local development, `run_local_server.sh` sets both tickers to **1 minute** so you can watch memory move through the tiers without waiting.

## Sizing workers

Each chain-break task costs one embedding comparison and sometimes LLM calls (gray-zone arbitration, then formation). Two workers per replica comfortably absorb typical chat workloads; the queue is the buffer. Scale worker count (or replicas: workers coordinate through Redis, so replicas just add capacity) when:

- `cognitive_work_queue` depth grows steadily (watch queue metrics)
- Chains appear long after their conversations ended

Do not scale workers past your LLM rate budget: more workers draining the queue into a rate-limited LLM just moves the wait.

## The rate-limit interaction

The knobs that must move together:

```
MEMORY_WORKER_COUNT × replicas   →  concurrent LLM demand
LLM_RATE_LIMIT_PER_MINUTE        →  supply ceiling
```

Because task retries are **LIFO with no backoff** (up to 3 attempts), an LLM outage or an undersized rate limit produces a hot retry loop. Keep the circuit breaker on (`LLM_CIRCUIT_BREAKER_THRESHOLD`, default 5) and alert on `llm_fallback_calls_total{reason="rate_limited"}`.

## Tuning the pipeline thresholds

All defaults are reasonable; change one at a time and watch the [metrics](../reference/metrics.md).

| Goal | Knob | Direction |
|---|---|---|
| Fewer, larger chains | `CHAIN_SIM_LOW` (0.52) | Lower it (breaks require stronger topic shifts) |
| More, finer chains | `CHAIN_SIM_HIGH` (0.72) | Raise it (more gray zone, more LLM arbitration cost) |
| Less junk stored | `MTM_QUALITY_MIN_SCORE` (0.5) | Raise it |
| More aggressive dedupe | `SESSION_MERGE_MIN_CONFIDENCE` (0.7) | Lower it (risk: unrelated topics merged) |
| Bigger conversation batches | `STM_MAX_EVENTS_BEFORE_FLUSH` (20) | Raise it (longer single-topic sessions stay whole) |
| More selective graph | `MEMORY_OS_MTM_HEAT_THRESHOLD` (0.3) | Raise it |
| Longer memory retention | `MTM_ARCHIVE_SCAN_DAYS` (7) / `MTM_FREEZING_POINT` (0.1) | Raise days or lower floor |

## What to monitor

```bash
curl -s http://localhost:8080/metrics | grep -E "memos_|stm_cache|llm_fallback|cosine"
```

The signals that matter operationally:

- `stm_cache_ops_total{result="error"}`: Redis trouble
- `memos_promoter_chains_evaluated_total` vs `..._promoted_total`: promotion selectivity drift
- `memos_heat_score_distribution`: watch before/after any heat-parameter change
- `llm_fallback_calls_total`: provider degradation (heuristic fallbacks lower chain quality silently)

Per-task outcomes live in Redis (`task_results:v1:{tenant}:{user}:{agent}:{taskId}`, 1-hour TTL) for spot-checking specific failures; the [Debug Tools](debug-tools.md) cover deeper inspection.
