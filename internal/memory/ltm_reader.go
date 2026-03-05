package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
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
func (r *LTMReader) FetchContext(ctx context.Context, tenantID string, startNodeIDs []string) (*GraphExtraction, error) {
	if len(startNodeIDs) == 0 {
		return &GraphExtraction{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	// This AQL query takes the array of start nodes (e.g. ["Concepts/customer_churn", "Identities/agent_007"])
	// and traverses OUTBOUND from them along the MemoryEdges collection up to depth 2.
	// It filters for minimum confidence and sorts by weight/heat to get the most relevant connections.
	query := `
		FOR startNode IN @startNodes
			// 1..2 means depth 1 to 2 hops. ANY means outbound or inbound.
			FOR v, e, p IN 1..2 ANY startNode MemoryEdges
				FILTER e != null && e.confidence >= 0.5
				SORT e.weight DESC, e.heat_score DESC
				LIMIT 50
				// Return the connected node and the edge that connects them
				RETURN DISTINCT {
					node: v,
					edge: e
				}
	`

	bindVars := map[string]interface{}{
		"startNodes": startNodeIDs,
	}

	// Make sure we have a metric for this
	startTimer := time.Now()
	cursor, err := r.db.Query(ctx, query, bindVars)
	if err != nil {
		slog.Error("Failed to execute AQL fetch query", slog.String("error", err.Error()), slog.Any("query_vars", bindVars))
		return nil, fmt.Errorf("failed to query arangodb: %w", err)
	}
	defer cursor.Close()

	slog.Debug("AQL query executed", slog.Duration("duration", time.Since(startTimer)))

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

		// The Node ID from ArangoDB includes the collection prefix (e.g. "Concepts/customer_churn" for _id),
		// but `_key` is just "customer_churn". We'll use _key for ID and deduce label from _id.
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

	return extraction, nil
}

func determineLabelFromID(fullID string) string {
	// fullID is typically "CollectionName/Key"
	for i, c := range fullID {
		if c == '/' {
			return fullID[:i]
		}
	}
	return "Concepts" // Fallback
}

func stripCollectionPrefix(fullID string) string {
	for i, c := range fullID {
		if c == '/' {
			return fullID[i+1:]
		}
	}
	return fullID
}
