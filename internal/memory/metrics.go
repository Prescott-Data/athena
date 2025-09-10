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
	)
}
