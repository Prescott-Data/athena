# Metrics

Athena exposes Prometheus metrics at `GET /metrics` on port 8080 (no auth required). Two naming families exist: cognitive-pipeline metrics are unprefixed; promoter, LTM, blob, and analytics metrics carry the `memos_` prefix.

## Pipeline (worker / STM path)

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `stm_cache_ops_total` | counter | `op`, `result` | STM Redis operations; `result="error"` signals Redis trouble |
| `embedding_latency_seconds` | histogram | | Embedding-creation latency (provider round trip) |
| `milvus_op_latency_seconds` | histogram | | Milvus operation latency |
| `cosine_similarity_distribution` | histogram | | Similarity scores from chain-break checks (0.0–1.0 buckets); shows where your traffic sits relative to the 0.52/0.72 gates |
| `cosine_gate_decisions_total` | counter | `decision` (`high`/`low`/`gray_zone`), `result` (`continue`/`new_chain`) | Chain-break gate outcomes; a high `gray_zone` share means high LLM arbitration cost |
| `llm_fallback_calls_total` | counter | `reason` (`gray_zone`/`embedding_failure`/`rate_limited`/`circuit_breaker`), `result` | LLM fallback calls; `rate_limited`/`circuit_breaker` reasons are your outage signal |
| `dialogue_chain_decision_latency_seconds` | histogram | | End-to-end chain-break decision latency |

## Promoter and heat

| Metric | Type | Meaning |
|---|---|---|
| `memos_promoter_chains_evaluated_total` | counter | Chains scored per promoter pass (cumulative) |
| `memos_promoter_chains_promoted_total` | counter | Chains that crossed the threshold; the ratio to evaluated is your promotion selectivity |
| `memos_heat_score_distribution` | histogram | Heat scores at evaluation time (0.1-wide buckets); watch before/after tuning any `HEAT_*` parameter |

## LTM writes (graph extraction)

| Metric | Type | Meaning |
|---|---|---|
| `memos_extractor_llm_duration_seconds` | histogram | Triple-extraction LLM latency |
| `memos_extractor_schema_failures_total` | counter | LLM returned invalid JSON; promotion aborted for that chain |
| `memos_ltm_arango_upsert_duration_seconds` | histogram | AQL UPSERT latency |
| `memos_ltm_nodes_written_total` | counter | Nodes upserted |
| `memos_ltm_edges_written_total` | counter | Edges upserted |
| `memos_ltm_edge_interceptions_total` | counter | Rogue relations auto-corrected to `RELATES_TO`; a rising rate means the extractor is drifting from the [whitelist](../concepts/promotion-and-knowledge-graph.md) |

## LTM reads (retrieval enrichment)

| Metric | Type | Meaning |
|---|---|---|
| `memos_ltm_fetch_duration_seconds` | histogram | Graph traversal latency during retrieval |
| `memos_ltm_nodes_read_total` / `memos_ltm_edges_read_total` | counter | Traversal result volume |
| `memos_ltm_fetch_errors_total` | counter | AQL traversal errors |
| `memos_ltm_fetch_no_results_total` | counter | Entity start nodes existed but traversal returned nothing; high values suggest a sparse graph or over-strict confidence filtering |

## Blob storage

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memos_blob_storage_ops_total` | counter | `operation` (`upload`/`download`/`delete`), `provider`, `status` | Object-store operations |
| `memos_blob_payload_bytes` | histogram | | Payload sizes; watch for events that should have been blobs |

## Analytics

| Metric | Type | Meaning |
|---|---|---|
| `memos_analytics_pregel_duration_seconds` | histogram | Community-detection runtime |
| `memos_analytics_bridge_calc_duration_seconds` | histogram | Bridge-scoring runtime |
| `memos_analytics_bridges_found_total` | counter | Bridge entities identified per run |

## A starter alert set

```yaml
- alert: AthenaRedisErrors
  expr: rate(stm_cache_ops_total{result="error"}[5m]) > 0
- alert: AthenaLLMDegraded
  expr: rate(llm_fallback_calls_total{reason=~"rate_limited|circuit_breaker"}[10m]) > 0.1
- alert: AthenaExtractionFailing
  expr: rate(memos_extractor_schema_failures_total[30m]) > 0.05
- alert: AthenaLTMTraversalErrors
  expr: rate(memos_ltm_fetch_errors_total[10m]) > 0
```

Standard Go runtime and process metrics (`go_*`, `process_*`) are exported alongside; scrape configuration is ordinary Prometheus (`/metrics`, no credentials).
