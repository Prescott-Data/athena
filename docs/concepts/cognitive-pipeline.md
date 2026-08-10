# The Cognitive Pipeline

Between "event stored" and "chain formed" sits the cognitive pipeline: a task queue, a pool of workers, and a topic-boundary detector. This page covers how work is scheduled and how Athena decides that a conversation has moved on.

## Enqueueing

Storing an event returns immediately; cognition is asynchronous. On every stored **user message** (role `user`, type `message`), the API enqueues a `cognitive_chain_check` task. Agent responses, thoughts, actions, and observations do not trigger checks: topic boundaries are defined by what the *user* does.

### The two-level queue

Tasks don't go into one global list. Each `(tenant, user, agent)` scope has its own Redis list, and a global dispatcher list holds *queue names*:

```
LPUSH memory_processing_queue:v1:{tenant}:{user}:{agent}  <task JSON>
LPUSH cognitive_work_queue                                 <that queue's name>
```

Workers block on the dispatcher (`BRPop cognitive_work_queue, 1s`), receive a queue name, then `RPop` one task from that scoped queue. **Fair scheduling falls out of the design:** a user hammering the API queues many tasks, but each task costs one dispatcher entry, so other users' entries interleave rather than starve.

Task envelopes carry `{id, type, payload, enqueued_at}`. Results are recorded at `task_results:v1:{tenant}:{user}:{agent}:{taskId}` with a 1-hour TTL. `STM_TASK_TIMEOUT` (default **300s**) bounds a task for monitoring purposes.

!!! warning "Retries are LIFO with no backoff"
    A failed task is re-`LPUSH`ed to the *head* of its queue, up to **3 attempts**, with no delay. During an LLM outage this becomes a tight retry loop, so budget your `LLM_RATE_LIMIT_PER_MINUTE` accordingly. (Known debt, tracked in the repo.)

## Workers

`MEMORY_WORKER_COUNT` goroutines (default **2**) consume the queue; `ENABLE_MEMORY_WORKERS=false` disables cognition entirely (Athena degrades to a plain STM store). Workers are stateless. Coordination happens through Redis, so server replicas share the load safely.

## Chain-break detection

Each task answers one question: *does the newest user message continue the current topic, or start a new one?*

**1. Fetch context.** The worker reads the STM window and locates the two most recent **user** messages. If both carry the same `workflow_id` or `execution_id`, the check is skipped: steps of one automated execution are never topic breaks.

**2. Compare embeddings.** Cosine similarity between the two messages' embeddings, then a three-way gate:

```
similarity ≥ CHAIN_SIM_HIGH (0.72)  →  same topic, chain continues
similarity < CHAIN_SIM_LOW  (0.52)  →  topic break, cut the segment
otherwise (the gray zone)           →  ask the LLM
```

**3. Gray-zone arbitration.** Similarity between 0.52 and 0.72 is genuinely ambiguous ("also, by the way..." questions live here), so a small LLM prompt decides:

> *"You are a topic boundary detector... 'Same topic' = same subject, task, or line of inquiry... A question about a DIFFERENT technical area = topic change... Respond with only `true` or `false`."*

`true` continues the chain; `false` breaks it. Embedding math handles the clear cases cheaply; the LLM is spent only on the boundary.

!!! note "Thresholds are env-tunable"
    `CHAIN_SIM_HIGH` / `CHAIN_SIM_LOW` default to **0.72 / 0.52**. (The Go constants `HighSimilarityThreshold = 0.9` / `LowSimilarityThreshold = 0.7` exist in the source but the env-var defaults are what actually gate production behavior.)

**4. Force flush.** Independently of similarity, if the STM window has accumulated `STM_MAX_EVENTS_BEFORE_FLUSH` events (default **20**), the worker partitions unconditionally. A marathon single-topic conversation still becomes chains instead of growing without bound.

## On a break: partition → MTM formation

When a break is declared, the worker cuts the completed segment out of the STM window (the new message stays; it starts the next segment), trims Redis, and hands the segment to [Chain Formation](chain-formation.md): topic analysis, summarization, the quality gate, and merge-or-create into `cognitive_chains`.

## Observing the pipeline

Prometheus counters cover every stage: queue depth, chain-break decisions, LLM fallbacks, formation outcomes (`memos_*`, see [Metrics](../reference/metrics.md)). Per-task outcomes are in the `task_results:*` keys for one hour. For tuning guidance (thresholds, worker count, rate limits) see [Workers & Schedulers](../guides/workers-and-schedulers.md).
