package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

// SearchByQuery resolves a free-text query to matching ArangoDB node IDs (via name keyword search)
// and then calls FetchContext to retrieve the surrounding sub-graph.
func (r *LTMReader) SearchByQuery(ctx context.Context, tenantID, query string) (*GraphExtraction, error) {
	keywords := extractLTMKeywords(query)
	if len(keywords) == 0 {
		return &GraphExtraction{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	nodeIDSet := make(map[string]bool)
	for _, kw := range keywords {
		ids, err := r.findNodeIDsByKeyword(ctx, kw)
		if err != nil {
			slog.Warn("LTM keyword search failed", slog.String("keyword", kw), slog.String("error", err.Error()))
			continue
		}
		for _, id := range ids {
			nodeIDSet[id] = true
		}
	}

	if len(nodeIDSet) == 0 {
		return &GraphExtraction{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	startNodes := make([]string, 0, len(nodeIDSet))
	for id := range nodeIDSet {
		startNodes = append(startNodes, id)
	}

	return r.FetchContext(ctx, tenantID, startNodes)
}

// findNodeIDsByKeyword searches all node collections for documents whose name contains the keyword.
func (r *LTMReader) findNodeIDsByKeyword(ctx context.Context, keyword string) ([]string, error) {
	query := `
		FOR doc IN UNION(
			(FOR d IN Concepts    RETURN d),
			(FOR d IN Identities  RETURN d),
			(FOR d IN Projects    RETURN d),
			(FOR d IN Tools       RETURN d)
		)
		FILTER CONTAINS(LOWER(doc.name), @kw)
		LIMIT 5
		RETURN doc._id
	`
	cursor, err := r.db.Query(ctx, query, map[string]interface{}{"kw": keyword})
	if err != nil {
		return nil, fmt.Errorf("keyword search AQL failed: %w", err)
	}
	defer cursor.Close()

	var ids []string
	for {
		var id string
		_, err := cursor.ReadDocument(ctx, &id)
		if driver.IsNoMoreDocuments(err) {
			break
		}
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// extractLTMKeywords splits a query into lowercase keywords, skipping stop words and short tokens.
func extractLTMKeywords(query string) []string {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true,
		"this": true, "from": true, "have": true, "been": true, "will": true,
		"what": true, "when": true, "where": true, "which": true, "were": true,
		"they": true, "their": true, "there": true, "about": true, "also": true,
		"into": true, "over": true, "after": true, "before": true, "some": true,
		"than": true, "then": true, "each": true, "such": true, "does": true,
	}
	var keywords []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		w = strings.Trim(w, ".,;:!?\"'()")
		if len(w) >= 4 && !stopWords[w] {
			keywords = append(keywords, w)
		}
	}
	return keywords
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
