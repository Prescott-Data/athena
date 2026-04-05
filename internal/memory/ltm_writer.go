package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Prescott-Data/athena/pkg/memory"
	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
	"github.com/prometheus/client_golang/prometheus"
)

// LTMWriter handles writing extracted graph data to ArangoDB.
type LTMWriter struct {
	db        driver.Database
	analytics *memory.AnalyticsEngine
}

// NewLTMWriter creates a new LTMWriter connected to the specified ArangoDB instance.
func NewLTMWriter(ctx context.Context, dbURL, user, pass, dbName string) (*LTMWriter, error) {
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
		return nil, fmt.Errorf("database %s does not exist. Run InitializeLTMGraph first", dbName)
	}

	db, err := client.Database(ctx, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbName, err)
	}

	return &LTMWriter{
		db:        db,
		analytics: memory.NewAnalyticsEngine(db),
	}, nil
}

// TriggerCommunityDetection starts a background community detection job across the LTM graph,
// and if successful, immediately follows up by calculating the bridge entities.
func (w *LTMWriter) TriggerCommunityDetection(ctx context.Context) error {
	err := w.analytics.RunCommunityDetection(ctx)
	if err != nil {
		return err
	}

	// Synchronously calculate bridge entities based on the new community identifiers
	return w.analytics.CalculateBridgeEntities(ctx)
}

// WriteExtractionToGraph takes a parsed graph extraction and upserts nodes and edges into ArangoDB.
func (w *LTMWriter) WriteExtractionToGraph(ctx context.Context, extraction *GraphExtraction, heatScore float64) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Process Nodes (Planets)
	// We iterate over the nodes and execute an UPSERT against their specific collection (based on Label)
	for _, node := range extraction.Nodes {
		collectionName := node.Label // Expected: "Identities", "Concepts", "Tools", "Projects"

		// Ensure collection is valid according to our definition
		if !isValidNodeCollection(collectionName) {
			slog.Warn("Invalid node collection label",
				slog.String("node_id", node.ID),
				slog.String("attempted_label", collectionName))
			continue
		}

		query := fmt.Sprintf(`
			UPSERT { _key: @key }
			INSERT { _key: @key, name: @name, created_at: @now, last_seen: @now }
			UPDATE { last_seen: @now }
			IN %s
		`, collectionName) // Collection names cannot be bind variables in ArangoDB AQL

		bindVars := map[string]interface{}{
			"key":  node.ID,
			"name": node.Name,
			"now":  now,
		}

		timer := prometheus.NewTimer(LTMArangoUpsertDuration)
		cursor, err := w.db.Query(ctx, query, bindVars)
		timer.ObserveDuration()
		if err != nil {
			slog.Error("Failed to upsert to ArangoDB",
				slog.String("error", err.Error()),
				slog.Any("query_vars", bindVars))
			continue
		}
		LTMNodesWritten.Inc()
		cursor.Close()
	}

	// 2. Process Edges (Gravity)
	// For MemoryEdges, we do a balanced merge where weight increases and confidence averages out over time.
	for _, edge := range extraction.Edges {
		// Try to resolve the full _from and _to paths (Collection/Key)
		// We have to lookup the node's label from the extraction.Nodes array since edges only contain the ID
		fromCollection := lookupNodeLabel(extraction.Nodes, edge.From)
		toCollection := lookupNodeLabel(extraction.Nodes, edge.To)

		if fromCollection == "" || toCollection == "" {
			slog.Warn("Could not resolve collection for edge",
				slog.String("from", edge.From),
				slog.String("to", edge.To),
				slog.String("relation", edge.Relation))
			continue
		}

		fromID := fmt.Sprintf("%s/%s", fromCollection, edge.From)
		toID := fmt.Sprintf("%s/%s", toCollection, edge.To)

		// Edge Validation & Interceptor
		if !isValidEdgeRelation(edge.Relation) {
			LTMEdgeInterceptions.Inc()
			slog.Warn("Rogue edge relation intercepted",
				slog.String("from", edge.From),
				slog.String("to", edge.To),
				slog.String("rogue_verb", edge.Relation),
				slog.String("auto_corrected_to", "RELATES_TO"))

			if edge.ContextNuance == "" {
				edge.ContextNuance = edge.Relation
			} else {
				edge.ContextNuance = fmt.Sprintf("%s (%s)", edge.ContextNuance, edge.Relation)
			}

			edge.Relation = "RELATES_TO"
		}

		query := `
			UPSERT { _from: @from, _to: @to, relation: @relation }
			INSERT {
				_from: @from,
				_to: @to,
				relation: @relation,
				context_nuance: @context_nuance,
				confidence: @confidence,
				heat_score: @heat,
				weight: 1,
				created_at: @now,
				last_seen: @now
			}
			UPDATE {
				context_nuance: @context_nuance,
				confidence: (OLD.confidence + @confidence) / 2,
				weight: OLD.weight + 1,
				heat_score: @heat,
				last_seen: @now
			}
			IN MemoryEdges
		`

		bindVars := map[string]interface{}{
			"from":           fromID,
			"to":             toID,
			"relation":       edge.Relation,
			"context_nuance": edge.ContextNuance,
			"confidence":     edge.Confidence,
			"heat":           heatScore,
			"now":            now,
		}

		timer := prometheus.NewTimer(LTMArangoUpsertDuration)
		cursor, err := w.db.Query(ctx, query, bindVars)
		timer.ObserveDuration()
		if err != nil {
			slog.Error("Failed to upsert to ArangoDB",
				slog.String("error", err.Error()),
				slog.Any("query_vars", bindVars))
			continue
		}
		LTMEdgesWritten.Inc()
		cursor.Close()
	}

	return nil
}

// Helper to validate allowed node collections
func isValidNodeCollection(label string) bool {
	valid := map[string]bool{
		"Identities": true,
		"Concepts":   true,
		"Tools":      true,
		"Projects":   true,
	}
	return valid[label]
}

// Helper to look up a node's label (collection) based on its ID from the extracted nodes
func lookupNodeLabel(nodes []GraphNode, id string) string {
	for _, n := range nodes {
		if n.ID == id {
			return n.Label
		}
	}
	return ""
}

// Helper to validate allowed edge relations
func isValidEdgeRelation(relation string) bool {
	valid := map[string]bool{
		"USES":               true,
		"WORKS_ON":           true,
		"BUILT_FOR_CLIENT":   true,
		"STRUGGLES_WITH":     true,
		"EXHIBITS":           true,
		"EXPRESSED_INTEREST": true,
		"RELATES_TO":         true,
	}
	return valid[relation]
}
