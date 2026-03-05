package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
	"github.com/prometheus/client_golang/prometheus"
)

// LTMReader handles querying extracted graph data from ArangoDB.
type LTMReader struct {
	db driver.Database
}

// NewLTMReader creates a new LTMReader connected to the specified ArangoDB instance.
func NewLTMReader(ctx context.Context, dbURL, user, pass, dbName string) (*LTMReader, error) {
	conn, err := http.NewConnection(http.ConnectionConfig{
		Endpoints: []string{dbURL},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create arangodb connection: %w", err)
	}

	client, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(user, pass),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create arangodb client: %w", err)
	}

	exists, err := client.DatabaseExists(ctx, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %w", err)
	}

	if !exists {
		return nil, fmt.Errorf("database %s does not exist", dbName)
	}

	db, err := client.Database(ctx, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbName, err)
	}

	return &LTMReader{
		db: db,
	}, nil
}

// FetchContext retrieves a sub-graph starting from the provided entity node IDs.
// It executes an AQL graph traversal (1-2 hops) from each start vertex via MemoryEdges,
// filtering by confidence >= 0.5 and ranking by weight and heat_score.
func (r *LTMReader) FetchContext(ctx context.Context, tenantID string, startNodeIDs []string) (*GraphExtraction, error) {
	if len(startNodeIDs) == 0 {
		return &GraphExtraction{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	slog.Debug("LTM fetch starting",
		slog.String("tenant_id", tenantID),
		slog.Int("start_nodes_count", len(startNodeIDs)),
		slog.Any("start_nodes", startNodeIDs),
	)

	// AQL traversal: from each start node, hop 1-2 edges along MemoryEdges.
	// Results are ranked by weight (frequency) and heat_score (recency).
	// Only edges with confidence >= 0.5 are returned.
	query := `
		FOR startNode IN @startNodes
			FOR v, e, p IN 1..2 ANY startNode MemoryEdges
				FILTER e != null && e.confidence >= 0.5
				SORT e.weight DESC, e.heat_score DESC
				LIMIT 50
				RETURN DISTINCT {
					node: v,
					edge: e
				}
	`

	bindVars := map[string]interface{}{
		"startNodes": startNodeIDs,
	}

	fetchStart := time.Now()
	timer := prometheus.NewTimer(LTMFetchDuration)
	cursor, err := r.db.Query(ctx, query, bindVars)
	timer.ObserveDuration()

	if err != nil {
		LTMFetchErrors.Inc()
		slog.Error("LTM AQL traversal failed",
			slog.String("tenant_id", tenantID),
			slog.String("error", err.Error()),
			slog.Any("start_nodes", startNodeIDs),
		)
		return nil, fmt.Errorf("failed to query arangodb: %w", err)
	}
	defer cursor.Close()

	nodesMap := make(map[string]GraphNode)
	edgesMap := make(map[string]GraphEdge)

	for {
		var result struct {
			Node struct {
				ID     string `json:"_key"`
				IDFull string `json:"_id"`
				Name   string `json:"name"`
			} `json:"node"`
			Edge struct {
				ID            string  `json:"_key"`
				From          string  `json:"_from"`
				To            string  `json:"_to"`
				Relation      string  `json:"relation"`
				ContextNuance string  `json:"context_nuance"`
				Confidence    float64 `json:"confidence"`
			} `json:"edge"`
		}

		_, err := cursor.ReadDocument(ctx, &result)
		if driver.IsNoMoreDocuments(err) {
			break
		}
		if err != nil {
			slog.Warn("Error reading AQL document", slog.String("error", err.Error()))
			continue
		}

		// The full ArangoDB ID is "Collection/Key" (e.g. "Concepts/customer_churn").
		// We store the key as ID and derive the label from the collection prefix.
		label := determineLabelFromID(result.Node.IDFull)
		nodesMap[result.Node.IDFull] = GraphNode{
			ID:    result.Node.ID,
			Label: label,
			Name:  result.Node.Name,
		}

		edgeKey := fmt.Sprintf("%s-%s-%s", result.Edge.From, result.Edge.Relation, result.Edge.To)
		edgesMap[edgeKey] = GraphEdge{
			From:          stripCollectionPrefix(result.Edge.From),
			To:            stripCollectionPrefix(result.Edge.To),
			Relation:      result.Edge.Relation,
			ContextNuance: result.Edge.ContextNuance,
			Confidence:    result.Edge.Confidence,
		}
	}

	extraction := &GraphExtraction{
		Nodes: make([]GraphNode, 0, len(nodesMap)),
		Edges: make([]GraphEdge, 0, len(edgesMap)),
	}
	for _, n := range nodesMap {
		extraction.Nodes = append(extraction.Nodes, n)
	}
	for _, e := range edgesMap {
		extraction.Edges = append(extraction.Edges, e)
	}

	// Record read-path metrics
	nodeCount := len(extraction.Nodes)
	edgeCount := len(extraction.Edges)
	LTMNodesRead.Add(float64(nodeCount))
	LTMEdgesRead.Add(float64(edgeCount))

	if nodeCount == 0 {
		LTMFetchNoResults.Inc()
		slog.Warn("LTM traversal returned 0 results despite entity start nodes",
			slog.String("tenant_id", tenantID),
			slog.Any("start_nodes", startNodeIDs),
		)
	} else {
		slog.Info("LTM fetch completed",
			slog.String("tenant_id", tenantID),
			slog.Int("start_nodes_count", len(startNodeIDs)),
			slog.Int("nodes_retrieved", nodeCount),
			slog.Int("edges_retrieved", edgeCount),
			slog.Duration("duration", time.Since(fetchStart)),
		)
	}

	return extraction, nil
}

// determineLabelFromID extracts the collection name from an ArangoDB full ID (e.g. "Concepts/foo" → "Concepts").
func determineLabelFromID(fullID string) string {
	for i, c := range fullID {
		if c == '/' {
			return fullID[:i]
		}
	}
	return "Concepts" // Fallback
}

// stripCollectionPrefix removes the collection prefix from an ArangoDB full ID (e.g. "Concepts/foo" → "foo").
func stripCollectionPrefix(fullID string) string {
	for i, c := range fullID {
		if c == '/' {
			return fullID[i+1:]
		}
	}
	return fullID
}
