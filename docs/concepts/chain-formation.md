# Chain Formation

When the [pipeline](cognitive-pipeline.md) cuts a finished topic segment out of STM, *chain formation* turns those raw events into a cognitive chain, or decides they aren't worth keeping. This page covers analysis, the quality gate, and merging.

## Topic & entity analysis

The segment's events are concatenated and analyzed, LLM first with heuristic keyword extraction as fallback:

- **Main topic** plus up to `TOPIC_ANALYSIS_MAX_TOPICS` (default **3**) subtopics, each with a confidence score; topics below `TOPIC_ANALYSIS_MIN_CONFIDENCE` (default **0.6**) are dropped
- **Entities** mentioned in the segment, stored on the chain document
- A written **summary** of the segment
- The analysis call is bounded by `TOPIC_ANALYSIS_LLM_TIMEOUT` (default **20s**)

## The quality gate

Not every segment becomes a chain: small talk, acknowledgments, and noise are filtered by `mtm_quality_validator`. Five metrics are computed and combined:

| Metric | Measures |
|---|---|
| Coherence (weight 0.4) | Text consistency and flow |
| Completeness (weight 0.3) | Coverage of the topic |
| Relevance (weight 0.3) | Alignment with user intent |
| Engagement | Depth of user↔agent exchange |
| Cognitive depth | Thought/action event density |

A chain is stored only if **all** of:

```
OverallScore ≥ MTM_QUALITY_MIN_SCORE   (default 0.5)
AND the summary is valid
AND (user engagement not required OR the user asked questions)
```

Validation runs in one of three modes (`ValidationModePermissive`, `Balanced` (default), `Strict`) selected via `MEMORY_OS_MTM_QUALITY_MODE` (`fast` | `balanced` | `thorough`). Failing the gate means the events stay in the durable log but no chain (and no embedding) is created.

## Merging

A returning topic should warm the existing chain, not spawn a duplicate. Before creating a new chain, the session manager searches the user's recent chains (window `SESSION_MAX_AGE_HOURS`, default **72h**, capped at `SESSION_MAX_CHAINS_PER_USER` = **100**) for merge candidates.

Each candidate gets a **merge confidence** built from the continuity analyzer's score with adjustments:

```
+0.10  if the time gap < 1 hour
+0.05  if the time gap < 6 hours
−0.10  if event-count difference > 10
result clamped to [0, 1]
```

The best candidate is merged into if **both** gates pass:

```
MergeConfidence ≥ SESSION_MERGE_MIN_CONFIDENCE   (default 0.7)
SimilarityScore ≥ SESSION_SIMILARITY_THRESHOLD   (default 0.6)
```

On merge, the chain's summary is regenerated over the combined content, entities are unioned, and its embedding is refreshed. Otherwise a new chain document is inserted.

## What a chain looks like

A `cognitive_chains` document (MongoDB) carries:

```
tenant_id / user_id / agent_id      scope
topic, summary, entities[]           the distilled content
quality scores                       from the gate
heat_score, recall_strength,         lifecycle state
last_accessed_at, status             (active | archived)
event references                     which cognitive_events belong to it
```

plus exactly one embedding in Milvus keyed by the chain ID: the handle by which [`SearchMemory`](../guides/semantic-search.md) finds it, and the first thing deleted when the chain is [archived](archival-and-lifecycle.md).

From here, the chain's fate is governed by its temperature: [Heat & Decay](heat-and-decay.md).
