package memory

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	MetricSTMCacheOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "stm_cache_ops_total", Help: "STM cache operations"},
		[]string{"op", "result"},
	)
	MetricEmbeddingLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "embedding_latency_seconds",
		Help:    "Latency for embedding creation",
		Buckets: prometheus.DefBuckets,
	})
	MetricMilvusLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "milvus_op_latency_seconds",
		Help:    "Latency for Milvus operations",
		Buckets: prometheus.DefBuckets,
	})

	// Cosine gate metrics
	MetricCosineSimilarity = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cosine_similarity_distribution",
		Help:    "Distribution of cosine similarity scores",
		Buckets: []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})
	MetricCosineGateDecisions = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "cosine_gate_decisions_total", Help: "Cosine gate decision outcomes"},
		[]string{"decision", "result"}, // decision: high/low/gray_zone, result: continue/new_chain
	)
	MetricLLMFallbackCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "llm_fallback_calls_total", Help: "LLM fallback calls and results"},
		[]string{"reason", "result"}, // reason: gray_zone/embedding_failure/rate_limited/circuit_breaker, result: success/error
	)
	MetricDialogueChainLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "dialogue_chain_decision_latency_seconds",
		Help:    "Latency for dialogue chain decision process",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0},
	})

	// Promoter Metrics
	PromoterChainsEvaluated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_promoter_chains_evaluated_total",
		Help: "Total number of MTM chains evaluated by the promoter",
	})
	PromoterChainsPromoted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_promoter_chains_promoted_total",
		Help: "Total number of MTM chains successfully promoted to LTM",
	})
	HeatScoreDistribution = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memos_heat_score_distribution",
		Help:    "Distribution of heat scores for evaluated chains",
		Buckets: prometheus.LinearBuckets(0.1, 0.1, 10),
	})

	// LTM Metrics
	ExtractorLLMDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memos_extractor_llm_duration_seconds",
		Help:    "Latency of the LLM extraction API call",
		Buckets: prometheus.DefBuckets,
	})
	ExtractorSchemaFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_extractor_schema_failures_total",
		Help: "Total number of times the LLM failed to return valid JSON",
	})
	LTMArangoUpsertDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memos_ltm_arango_upsert_duration_seconds",
		Help:    "Latency of ArangoDB AQL upsert queries",
		Buckets: prometheus.DefBuckets,
	})
	LTMNodesWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_ltm_nodes_written_total",
		Help: "Total number of Planet Nodes successfully upserted",
	})
	LTMEdgesWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_ltm_edges_written_total",
		Help: "Total number of Gravity Edges successfully upserted",
	})
	LTMEdgeInterceptions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memos_ltm_edge_interceptions_total",
		Help: "Total number of rogue LLM verbs intercepted and auto-corrected to RELATES_TO",
	})

	// Blob Storage Metrics
	BlobStorageOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "memos_blob_storage_ops_total",
			Help: "Total number of Blob Storage operations",
		},
		[]string{"operation", "provider", "status"}, // operation: upload/download/delete, provider: s3/minio/gcs/local, status: success/error
	)
	BlobPayloadBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "memos_blob_payload_bytes",
			Help:    "Size of payloads written to Blob Storage",
			Buckets: []float64{1024, 10240, 102400, 1048576, 5242880, 10485760}, // 1KB, 10KB, 100KB, 1MB, 5MB, 10MB
		},
		[]string{"provider"},
	)
)

func init() {
	prometheus.MustRegister(
		MetricSTMCacheOps,
		MetricEmbeddingLatency,
		MetricMilvusLatency,
		MetricCosineSimilarity,
		MetricCosineGateDecisions,
		MetricLLMFallbackCalls,
		MetricDialogueChainLatency,
		PromoterChainsEvaluated,
		PromoterChainsPromoted,
		HeatScoreDistribution,
		ExtractorLLMDuration,
		ExtractorSchemaFailures,
		LTMArangoUpsertDuration,
		LTMNodesWritten,
		LTMEdgesWritten,
		LTMEdgeInterceptions,
		BlobStorageOps,
		BlobPayloadBytes,
	)
}
