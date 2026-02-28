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
func (e *AnalyticsEngine) CalculateBridgeEntities(ctx context.Context) error {
	slog.Info("Starting LTM Bridge Entity Calculation...")

	// We calculate the bridge score by finding all distinct community IDs a node touches
	// via its incoming or outgoing MemoryEdges. A node is a bridge if it connects to communities
	// other than its own. The score is the number of distinct *other* communities it touches.
	// Since node IDs span multiple collections, we can query them dynamically or iteratively.
	// ArangoDB's traversal allows us to do this across all nodes.

	query := `
		// Collect all nodes from the four node collections
		FOR node IN UNION(Identities, Concepts, Tools, Projects)
			// Filter out nodes that don't have a community_id
			FILTER HAS(node, "community_id") AND node.community_id != null
			
			// Find all distinct communities this node connects to (both incoming and outgoing)
			LET connected_communities = (
				FOR v, edge IN 1..1 ANY node MemoryEdges
					FILTER HAS(v, "community_id") AND v.community_id != null AND v.community_id != node.community_id
					RETURN DISTINCT v.community_id
			)
			
			LET bridge_score = LENGTH(connected_communities)
			LET is_bridge = bridge_score > 0
			
			// Update the node depending on its collection
			// We parse the collection part of the node's _id
			LET col = PARSE_IDENTIFIER(node._id).collection
			
			// Only perform update if the node is actually a bridge to avoid massive write ops
			FILTER is_bridge == true
			
			// Execute the dynamic update
			UPDATE node._key WITH { 
				is_bridge: true, 
				bridge_score: bridge_score 
			} IN col
			
			RETURN { id: node._id, bridge_score: bridge_score }
	`

	timer := time.Now()

	// Start an AQL query
	cursor, err := e.db.Query(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to execute bridge calculation AQL: %w", err)
	}
	defer cursor.Close()

	bridgeCount := 0
	for cursor.HasMore() {
		var doc map[string]interface{}
		_, err := cursor.ReadDocument(ctx, &doc)
		if err != nil {
			return fmt.Errorf("failed to read cursor document: %w", err)
		}
		bridgeCount++
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
