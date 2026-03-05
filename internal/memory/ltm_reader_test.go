package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arangodb/go-driver"
)

// --- Mock ArangoDB types for ltm_reader tests (prefixed to avoid collision with ltm_writer_test.go) ---

type readerMockDatabase struct {
	driver.Database
	queryResult []interface{}
	queryError  error
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
	data, err := json.Marshal(c.docs[c.current])
	if err != nil {
		return driver.DocumentMeta{}, err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return driver.DocumentMeta{}, err
	}
	c.current++
	return driver.DocumentMeta{}, nil
}

func (c *readerMockCursor) Close() error { return nil }

func (m *readerMockDatabase) Query(ctx context.Context, query string, bindVars map[string]interface{}) (driver.Cursor, error) {
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
				"_key": "customer_churn",
				"_id":  "Concepts/customer_churn",
				"name": "Customer Churn",
			},
			"edge": map[string]interface{}{
				"_key":           "e1",
				"_from":          "Identities/sangalo",
				"_to":            "Concepts/customer_churn",
				"relation":       "EXPRESSED_INTEREST",
				"context_nuance": "",
				"confidence":     0.9,
			},
		},
	}

	reader := newTestLTMReader(mockDocs, nil)

	result, err := reader.FetchContext(context.Background(), "test-tenant", []string{"Identities/sangalo"})

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

	result, err := reader.FetchContext(context.Background(), "test-tenant", []string{})
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

	result, err := reader.FetchContext(context.Background(), "test-tenant", []string{"Concepts/foo"})
	if err == nil {
		t.Fatal("Expected FetchContext to return an error when ArangoDB fails")
	}
	if result != nil {
		t.Error("Expected nil result when ArangoDB returns an error")
	}
}
