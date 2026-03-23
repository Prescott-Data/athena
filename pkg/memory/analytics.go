package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Graph Analytics Metrics
	AnalyticsPregelDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memos_analytics_pregel_duration_seconds",
		Help:    "Latency of the Pregel Label Propagation job for Community Detection",
		Buckets: prometheus.DefBuckets,
	})
	AnalyticsBridgeCalcDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memos_analytics_bridge_calc_duration_seconds",
		Help:    "Latency of the AQL Bridge Entity calculation job",
		Buckets: prometheus.DefBuckets,
	})
	AnalyticsBridgesFound = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "memos_analytics_bridges_found_total",
		Help: "Total number of distinct bridge entities discovered in the graph",
	})
)

func init() {
	prometheus.MustRegister(
		AnalyticsPregelDuration,
		AnalyticsBridgeCalcDuration,
		AnalyticsBridgesFound,
	)
}

// AnalyticsEngine handles graph-wide analytical processing like Community Detection.
type AnalyticsEngine struct {
	db driver.Database
}

// NewAnalyticsEngine creates a new analytics engine connected to the LTM database.
func NewAnalyticsEngine(db driver.Database) *AnalyticsEngine {
	return &AnalyticsEngine{db: db}
}

// RunCommunityDetection uses ArangoDB's native Pregel Label Propagation algorithm
// to cluster nodes into communities based on edge weights.
func (e *AnalyticsEngine) RunCommunityDetection(ctx context.Context) error {
	slog.Info("Starting LTM Community Detection via Pregel Label Propagation...")

	// The collections to run community detection on
	vertexCollections := []string{"Identities", "Concepts", "Tools", "Projects"}
	edgeCollections := []string{"MemoryEdges"}

	// Pregel Label Propagation parameters
	// "maxGSS" defines the maximum number of global supersteps (iterations).
	// "resultField" specifies the attribute name where the community ID will be stored on each vertex.
	params := map[string]interface{}{
		"maxGSS":      50,
		"resultField": "community_id",
		"store":       true,
	}

	opts := driver.PregelJobOptions{
		Algorithm:         "labelpropagation",
		VertexCollections: vertexCollections,
		EdgeCollections:   edgeCollections,
		Params:            params,
	}

	pregelDB, ok := e.db.(driver.DatabasePregels)
	if !ok {
		return fmt.Errorf("database does not support Pregel APIs")
	}

	// 1. Start the Pregel job
	jobID, err := pregelDB.StartJob(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to start Pregel community detection job: %w", err)
	}

	slog.Info("Pregel job started", slog.String("job_id", jobID))

	// 2. Poll for job completion
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Attempt to cancel the job if context expires
			pregelDB.CancelJob(context.Background(), jobID)
			return fmt.Errorf("community detection cancelled due to context timeout/cancellation")
		case <-ticker.C:
			jobState, err := pregelDB.GetJob(ctx, jobID)
			if err != nil {
				return fmt.Errorf("failed to fetch Pregel job status: %w", err)
			}

			switch jobState.State {
			case "done":
				slog.Info("Pregel community detection completed successfully",
					slog.Int("vertex_count", int(jobState.VertexCount)),
					slog.Int("edge_count", int(jobState.EdgeCount)),
					slog.Float64("total_runtime_s", jobState.TotalRuntime),
				)
				AnalyticsPregelDuration.Observe(jobState.TotalRuntime)
				return nil
			case "canceled":
				return fmt.Errorf("pregel job was canceled")
			case "error", "fatal error":
				return fmt.Errorf("pregel job failed: %v", jobState.State)
			default:
				// "running", "loading", "storing" - continue waiting
				slog.Debug("Pregel job is still running", slog.String("state", string(jobState.State)))
			}
		}
	}
}

// CalculateBridgeEntities iterates through all graph nodes to determine if they act as
// bridges between different communities, updating them with a bridge_score.
// This should be run synchronously after community detection completes.
//
// ArangoDB does not support dynamic collection names in DML statements, so we run one
// query per node collection instead of a single UNION query with a dynamic UPDATE target.
func (e *AnalyticsEngine) CalculateBridgeEntities(ctx context.Context) error {
	slog.Info("Starting LTM Bridge Entity Calculation...")

	// bridgeQuery runs for a single named collection. ArangoDB requires the collection
	// name in UPDATE to be a string literal, not a variable — hence one query per collection.
	bridgeQuery := func(collection string) string {
		return fmt.Sprintf(`
			FOR node IN %s
				FILTER HAS(node, "community_id") AND node.community_id != null
				LET connected_communities = (
					FOR v, edge IN 1..1 ANY node MemoryEdges
						FILTER HAS(v, "community_id") AND v.community_id != null
							AND v.community_id != node.community_id
						RETURN DISTINCT v.community_id
				)
				LET bridge_score = LENGTH(connected_communities)
				FILTER bridge_score > 0
				UPDATE node._key WITH { is_bridge: true, bridge_score: bridge_score } IN %s
				RETURN { id: node._id, bridge_score: bridge_score }
		`, collection, collection)
	}

	timer := time.Now()
	bridgeCount := 0

	for _, col := range []string{"Concepts", "Identities", "Projects", "Tools"} {
		cursor, err := e.db.Query(ctx, bridgeQuery(col), nil)
		if err != nil {
			return fmt.Errorf("bridge calculation failed for collection %s: %w", col, err)
		}

		for cursor.HasMore() {
			var doc map[string]interface{}
			_, err := cursor.ReadDocument(ctx, &doc)
			if err != nil {
				cursor.Close()
				return fmt.Errorf("failed to read bridge result from %s: %w", col, err)
			}
			bridgeCount++
		}
		cursor.Close()

		slog.Info("Bridge calculation complete for collection",
			slog.String("collection", col),
		)
	}

	duration := time.Since(timer)
	AnalyticsBridgeCalcDuration.Observe(duration.Seconds())
	AnalyticsBridgesFound.Set(float64(bridgeCount))

	slog.Info("Bridge Entity Calculation completed successfully",
		slog.Int("bridges_found", bridgeCount),
		slog.Float64("duration_s", duration.Seconds()),
	)

	return nil
}
