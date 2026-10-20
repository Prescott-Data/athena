package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// TriggerCommunityDetection runs bridge entity calculation across the LTM graph.
// Community detection via Pregel is attempted first; if it fails (e.g. ArangoDB 3.12+
// removed the Pregel API), we log a warning and proceed — community_id values are
// assumed to be pre-populated by the external igraph/Louvain K8s job from H4.
func (w *LTMWriter) TriggerCommunityDetection(ctx context.Context) error {
	if err := w.analytics.RunCommunityDetection(ctx); err != nil {
		slog.Warn("Pregel community detection unavailable — proceeding with pre-populated community_id values",
			slog.String("reason", err.Error()),
		)
	}

	// Synchronously calculate bridge entities based on existing community_id values.
	// To re-run community detection, use the H4 K8s job (industry-benchmark/experiments/h4/k8s-job.yaml).
	return w.analytics.CalculateBridgeEntities(ctx)
}

// WriteExtractionToGraph takes a parsed graph extraction and upserts nodes and edges
// into ArangoDB within the tenant and user's own graph: node keys carry a scope
// prefix and every node and edge records its tenant and user, so knowledge from one
// tenant is never merged with, or returned to, another.
//
// Every node and edge records the chains it was extracted from (sources), so erasing
// a chain can remove exactly the knowledge nothing else supports, and promoting the
// same chain again does not count as new evidence.
func (w *LTMWriter) WriteExtractionToGraph(ctx context.Context, tenantID, userID, chainID string, extraction *GraphExtraction, heatScore float64) error {
	if tenantID == "" || userID == "" {
		return fmt.Errorf("LTM writes need a tenant and user scope")
	}
	if chainID == "" {
		return fmt.Errorf("LTM writes need the source chain")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	failed := 0

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
			INSERT { _key: @key, entity_id: @entity, tenant_id: @tenant, user_id: @user, name: @name, sources: [@chain], created_at: @now, last_seen: @now }
			UPDATE { last_seen: @now, sources: UNION_DISTINCT(OLD.sources || [@legacy], [@chain]) }
			IN %s
		`, collectionName) // Collection names cannot be bind variables in ArangoDB AQL

		bindVars := map[string]interface{}{
			"key":    ltmScopedKey(tenantID, userID, node.ID),
			"entity": node.ID,
			"tenant": tenantID,
			"user":   userID,
			"name":   node.Name,
			"chain":  chainID,
			"legacy": legacySource,
			"now":    now,
		}

		timer := prometheus.NewTimer(LTMArangoUpsertDuration)
		cursor, err := w.db.Query(ctx, query, bindVars)
		timer.ObserveDuration()
		if err != nil {
			slog.Error("Failed to upsert to ArangoDB",
				slog.String("error", err.Error()),
				slog.Any("query_vars", bindVars))
			failed++
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

		fromID := fmt.Sprintf("%s/%s", fromCollection, ltmScopedKey(tenantID, userID, edge.From))
		toID := fmt.Sprintf("%s/%s", toCollection, ltmScopedKey(tenantID, userID, edge.To))

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
				tenant_id: @tenant,
				user_id: @user,
				relation: @relation,
				context_nuance: @context_nuance,
				confidence: @confidence,
				heat_score: @heat,
				weight: 1,
				sources: [@chain],
				created_at: @now,
				last_seen: @now
			}
			UPDATE {
				context_nuance: @context_nuance,
				confidence: POSITION(OLD.sources || [@legacy], @chain) ? OLD.confidence : (OLD.confidence + @confidence) / 2,
				weight: POSITION(OLD.sources || [@legacy], @chain) ? OLD.weight : OLD.weight + 1,
				sources: UNION_DISTINCT(OLD.sources || [@legacy], [@chain]),
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
			"tenant":         tenantID,
			"user":           userID,
			"chain":          chainID,
			"legacy":         legacySource,
		}

		timer := prometheus.NewTimer(LTMArangoUpsertDuration)
		cursor, err := w.db.Query(ctx, query, bindVars)
		timer.ObserveDuration()
		if err != nil {
			slog.Error("Failed to upsert to ArangoDB",
				slog.String("error", err.Error()),
				slog.Any("query_vars", bindVars))
			failed++
			continue
		}
		LTMEdgesWritten.Inc()
		cursor.Close()
	}

	if failed > 0 {
		// The promoter records a chain as promoted only on success, so it retries.
		return fmt.Errorf("%d of the extraction's graph writes failed", failed)
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

// ltmScopedKey returns the ArangoDB key of an entity in a tenant and user's graph.
// The scope is hashed so that any tenant and user ID yields a valid, fixed-length key
// prefix; the same entity in two scopes gets two separate nodes.
func ltmScopedKey(tenantID, userID, entityID string) string {
	return ltmScopePrefix(tenantID, userID) + entityID
}

func ltmScopePrefix(tenantID, userID string) string {
	// Length-prefixed, so no pair of IDs can produce another pair's encoding.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s|%d:%s", len(tenantID), tenantID, len(userID), userID)))
	return "s" + hex.EncodeToString(sum[:8]) + "_"
}

// legacySource stands for the unknown chains behind an entry written before sources
// were recorded. It is never erased, so such knowledge is not lost when a newer
// chain that also supports it is erased.
const legacySource = "pre-provenance"

// ltmCollections are the graph's node collections, then its edge collection.
var ltmCollections = []string{"Identities", "Concepts", "Tools", "Projects", "MemoryEdges"}

// EraseChains removes the given chains from the sources of the tenant and user's
// graph entries and deletes the entries no chain supports any more, then any edge
// left without an endpoint. It returns how many nodes and edges were deleted.
// Entries written before sources were recorded cannot be attributed and are kept.
func (w *LTMWriter) EraseChains(ctx context.Context, tenantID, userID string, chainIDs []string) (int, error) {
	if tenantID == "" || userID == "" || len(chainIDs) == 0 {
		return 0, nil
	}
	bind := map[string]interface{}{"tenant": tenantID, "user": userID, "ids": chainIDs}
	// Edges first, so no edge is ever left pointing at a removed node.
	cols := append([]string{"MemoryEdges"}, ltmCollections[:len(ltmCollections)-1]...)
	deleted := 0
	for _, col := range cols {
		weight := ""
		if col == "MemoryEdges" {
			weight = ", weight: MAX([1, d.weight - (LENGTH(d.sources) - LENGTH(remaining))])"
		}
		if _, err := w.run(ctx, fmt.Sprintf(`
			FOR d IN %s
				FILTER d.tenant_id == @tenant AND d.user_id == @user AND IS_ARRAY(d.sources)
				LET remaining = MINUS(d.sources, @ids)
				FILTER LENGTH(remaining) < LENGTH(d.sources)
				UPDATE d WITH { sources: remaining%s } IN %s
				COLLECT WITH COUNT INTO n RETURN n`, col, weight, col), bind); err != nil {
			return deleted, fmt.Errorf("failed to detach chains in %s: %w", col, err)
		}
		n, err := w.run(ctx, fmt.Sprintf(`
			FOR d IN %s
				FILTER d.tenant_id == @tenant AND d.user_id == @user AND IS_ARRAY(d.sources) AND LENGTH(d.sources) == 0
				REMOVE d IN %s
				COLLECT WITH COUNT INTO n RETURN n`, col, col), map[string]interface{}{"tenant": tenantID, "user": userID})
		if err != nil {
			return deleted, fmt.Errorf("failed to remove unsupported entries in %s: %w", col, err)
		}
		deleted += n
	}
	n, err := w.run(ctx, `
		FOR e IN MemoryEdges
			FILTER e.tenant_id == @tenant AND e.user_id == @user
			FILTER DOCUMENT(e._from) == null OR DOCUMENT(e._to) == null
			REMOVE e IN MemoryEdges
			COLLECT WITH COUNT INTO n RETURN n`, map[string]interface{}{"tenant": tenantID, "user": userID})
	if err != nil {
		return deleted, fmt.Errorf("failed to remove dangling edges: %w", err)
	}
	return deleted + n, nil
}

// EraseScope deletes the tenant and user's whole graph, or the tenant's when userID
// is empty, and returns how many nodes and edges were deleted.
func (w *LTMWriter) EraseScope(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" {
		return 0, fmt.Errorf("tenant_id is required")
	}
	deleted := 0
	for _, col := range ltmCollections {
		n, err := w.run(ctx, fmt.Sprintf(`
			FOR d IN %s
				FILTER d.tenant_id == @tenant AND (@user == "" OR d.user_id == @user)
				REMOVE d IN %s
				COLLECT WITH COUNT INTO n RETURN n`, col, col), map[string]interface{}{"tenant": tenantID, "user": userID})
		if err != nil {
			return deleted, fmt.Errorf("failed to erase %s: %w", col, err)
		}
		deleted += n
	}
	return deleted, nil
}

// run executes a query that returns a single count.
func (w *LTMWriter) run(ctx context.Context, query string, bind map[string]interface{}) (int, error) {
	cursor, err := w.db.Query(ctx, query, bind)
	if err != nil {
		return 0, err
	}
	defer cursor.Close()
	var n int
	if _, err := cursor.ReadDocument(ctx, &n); err != nil && !driver.IsNoMoreDocuments(err) {
		return 0, err
	}
	return n, nil
}
