package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arangodb/go-driver"
)

// --- Mock ArangoDB types for ltm_reader tests (prefixed to avoid collision with ltm_writer_test.go) ---

type readerMockDatabase struct {
	driver.Database
	queryResult []interface{}
	queryError  error
	queries     []string
	bindVars    []map[string]interface{}
}

type readerMockCursor struct {
	driver.Cursor
	docs    []interface{}
	current int
}

// ReadDocument returns the next document or NoMoreDocumentsError when exhausted.
func (c *readerMockCursor) ReadDocument(ctx context.Context, result interface{}) (driver.DocumentMeta, error) {
	if c.current >= len(c.docs) {
		// Return the exact sentinel that driver.IsNoMoreDocuments recognizes.
		return driver.DocumentMeta{}, driver.NoMoreDocumentsError{}
	}
	// Like the real driver, a document is consumed even if it fails to decode.
	doc := c.docs[c.current]
	c.current++
	data, err := json.Marshal(doc)
	if err != nil {
		return driver.DocumentMeta{}, err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return driver.DocumentMeta{}, err
	}
	return driver.DocumentMeta{}, nil
}

func (c *readerMockCursor) Close() error { return nil }

func (m *readerMockDatabase) Query(ctx context.Context, query string, bindVars map[string]interface{}) (driver.Cursor, error) {
	m.queries = append(m.queries, query)
	m.bindVars = append(m.bindVars, bindVars)
	if m.queryError != nil {
		return nil, m.queryError
	}
	return &readerMockCursor{docs: m.queryResult}, nil
}

func newTestLTMReader(docs []interface{}, queryErr error) *LTMReader {
	return &LTMReader{db: &readerMockDatabase{queryResult: docs, queryError: queryErr}}
}

// --- Tests ---

// TestLTMReader_FetchContext_ReturnNodesAndEdges is the primary regression test.
// It verifies that when ArangoDB returns graph data, FetchContext correctly
// maps them to GraphNode and GraphEdge structs — ensuring LTM is NEVER a stub.
func TestLTMReader_FetchContext_ReturnNodesAndEdges(t *testing.T) {
	mockDocs := []interface{}{
		map[string]interface{}{
			"node": map[string]interface{}{
				"_key":      ltmScopedKey("test-tenant", "test-user", "customer_churn"),
				"entity_id": "customer_churn",
				"_id":       "Concepts/" + ltmScopedKey("test-tenant", "test-user", "customer_churn"),
				"name":      "Customer Churn",
			},
			"edge": map[string]interface{}{
				"_key":           "e1",
				"_from":          "Identities/" + ltmScopedKey("test-tenant", "test-user", "sangalo"),
				"_to":            "Concepts/" + ltmScopedKey("test-tenant", "test-user", "customer_churn"),
				"relation":       "EXPRESSED_INTEREST",
				"context_nuance": "",
				"confidence":     0.9,
			},
		},
	}

	reader := newTestLTMReader(mockDocs, nil)

	result, err := reader.FetchContext(context.Background(), "test-tenant", "test-user", []string{"Identities/sangalo"})

	if err != nil {
		t.Fatalf("FetchContext returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("FetchContext returned nil — LTM retrieval stub has regressed")
	}
	if len(result.Nodes) == 0 {
		t.Error("REGRESSION: FetchContext returned 0 nodes even though ArangoDB returned data")
	}
	if len(result.Edges) == 0 {
		t.Error("REGRESSION: FetchContext returned 0 edges even though ArangoDB returned data")
	}

	node := result.Nodes[0]
	if node.ID != "customer_churn" {
		t.Errorf("Expected node ID 'customer_churn', got '%s'", node.ID)
	}
	if node.Label != "Concepts" {
		t.Errorf("Expected node label 'Concepts', got '%s'", node.Label)
	}
	if node.Name != "Customer Churn" {
		t.Errorf("Expected node name 'Customer Churn', got '%s'", node.Name)
	}

	edge := result.Edges[0]
	if edge.From != "sangalo" || edge.To != "customer_churn" {
		t.Errorf("Expected edge sangalo -> customer_churn without scope prefixes, got %s -> %s", edge.From, edge.To)
	}
	if edge.Relation != "EXPRESSED_INTEREST" {
		t.Errorf("Expected relation 'EXPRESSED_INTEREST', got '%s'", edge.Relation)
	}
	if edge.Confidence != 0.9 {
		t.Errorf("Expected confidence 0.9, got %f", edge.Confidence)
	}

	t.Logf("PASS: FetchContext returned %d nodes and %d edges", len(result.Nodes), len(result.Edges))
}

// TestLTMReader_FetchContext_EmptyStartNodes ensures an empty input returns gracefully.
func TestLTMReader_FetchContext_EmptyStartNodes(t *testing.T) {
	reader := newTestLTMReader(nil, nil)

	result, err := reader.FetchContext(context.Background(), "test-tenant", "test-user", []string{})
	if err != nil {
		t.Fatalf("FetchContext with empty start nodes should not error: %v", err)
	}
	if result == nil {
		t.Fatal("FetchContext should never return nil")
	}
	if len(result.Nodes) != 0 || len(result.Edges) != 0 {
		t.Error("Expected empty extraction for empty start nodes")
	}
}

// TestLTMReader_FetchContext_DBError verifies graceful error handling when ArangoDB fails.
func TestLTMReader_FetchContext_DBError(t *testing.T) {
	reader := newTestLTMReader(nil, driver.ArangoError{Code: 503, ErrorMessage: "unavailable"})

	result, err := reader.FetchContext(context.Background(), "test-tenant", "test-user", []string{"Concepts/foo"})
	if err == nil {
		t.Fatal("Expected FetchContext to return an error when ArangoDB fails")
	}
	if result != nil {
		t.Error("Expected nil result when ArangoDB returns an error")
	}
}

func TestLTMScopedKeysSeparateTenantsAndUsers(t *testing.T) {
	a := ltmScopedKey("acme", "alice", "python")
	if a == ltmScopedKey("globex", "alice", "python") || a == ltmScopedKey("acme", "bob", "python") {
		t.Fatal("the same entity in two scopes must map to two nodes")
	}
	if a != ltmScopedKey("acme", "alice", "python") {
		t.Fatal("scoped keys must be stable")
	}
	// A crafted ID cannot collide across scopes by embedding a separator.
	if ltmScopedKey("a", "b\x00c", "x") == ltmScopedKey("a\x00b", "c", "x") {
		t.Fatal("scope prefixes must not collide")
	}
}

func TestLTMReaderQueriesAreScoped(t *testing.T) {
	db := &readerMockDatabase{queryResult: []interface{}{"Concepts/x"}}
	reader := &LTMReader{db: db}
	if _, err := reader.SearchByQuery(context.Background(), "acme", "alice", "python deployment"); err != nil {
		t.Fatalf("SearchByQuery: %v", err)
	}
	if len(db.queries) == 0 {
		t.Fatal("expected queries")
	}
	for i, q := range db.queries {
		if db.bindVars[i]["tenant"] != "acme" || db.bindVars[i]["user"] != "alice" {
			t.Errorf("query %d is not bound to the caller's scope: %v", i, db.bindVars[i])
		}
		if strings.Contains(q, "PRUNE") && !strings.Contains(q, "PRUNE e != null &&") {
			t.Errorf("query %d prunes at depth 0, where e is null, and so returns nothing", i)
		}
		if !strings.Contains(q, "@tenant") || !strings.Contains(q, "@user") {
			t.Errorf("query %d does not filter by scope:\n%s", i, q)
		}
	}
}
