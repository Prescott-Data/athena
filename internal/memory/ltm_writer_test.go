package memory

import (
	"context"
	"testing"

	"github.com/arangodb/go-driver"
	"github.com/stretchr/testify/assert"
)

// mockCursor implements driver.Cursor for testing.
type mockCursor struct {
	driver.Cursor
}

func (m *mockCursor) Close() error {
	return nil
}

// mockDatabase implements driver.Database for testing, recording queries.
type mockDatabase struct {
	driver.Database // embed interface so we only have to implement what we use
	queries         []string
	bindVars        []map[string]interface{}
}

func (m *mockDatabase) Query(ctx context.Context, query string, bindVars map[string]interface{}) (driver.Cursor, error) {
	m.queries = append(m.queries, query)
	m.bindVars = append(m.bindVars, bindVars)
	return &mockCursor{}, nil
}

func TestWriteExtraction_Nodes(t *testing.T) {
	mockDB := &mockDatabase{}
	writer := &LTMWriter{db: mockDB}

	// Create dummy extraction with two nodes
	extraction := &GraphExtraction{
		Nodes: []GraphNode{
			{ID: "sangalo", Label: "Identities", Name: "Sangalo"},
			{ID: "openclaw", Label: "Projects", Name: "OpenClaw"},
			{ID: "invalid_label", Label: "Unknown", Name: "Invalid"}, // Should be skipped
		},
		Edges: nil,
	}

	heatScore := 0.75

	err := writer.WriteExtractionToGraph(context.Background(), extraction, heatScore)
	assert.NoError(t, err)

	// Since "Unknown" label is invalid, we should only have 2 queries executed.
	assert.Len(t, mockDB.queries, 2)
	assert.Len(t, mockDB.bindVars, 2)

	// Verify first query (Identities)
	q1 := mockDB.queries[0]
	assert.Contains(t, q1, "UPSERT { _key: @key }")
	assert.Contains(t, q1, "INSERT { _key: @key, name: @name, created_at: @now, last_seen: @now }")
	assert.Contains(t, q1, "UPDATE { last_seen: @now }")
	assert.Contains(t, q1, "IN Identities") // Verification of dynamic table placement

	// Verify first bindVars
	bv1 := mockDB.bindVars[0]
	assert.Equal(t, "sangalo", bv1["key"])
	assert.Equal(t, "Sangalo", bv1["name"])
	assert.NotEmpty(t, bv1["now"])

	// Verify second query (Projects)
	q2 := mockDB.queries[1]
	assert.Contains(t, q2, "IN Projects")

	bv2 := mockDB.bindVars[1]
	assert.Equal(t, "openclaw", bv2["key"])
	assert.Equal(t, "OpenClaw", bv2["name"])
}

func TestWriteExtraction_Edges(t *testing.T) {
	mockDB := &mockDatabase{}
	writer := &LTMWriter{db: mockDB}

	extraction := &GraphExtraction{
		Nodes: []GraphNode{
			{ID: "sangalo", Label: "Identities", Name: "Sangalo"},
			{ID: "openclaw", Label: "Projects", Name: "OpenClaw"},
		},
		Edges: []GraphEdge{
			{From: "sangalo", To: "openclaw", Relation: "WORKS_ON", ContextNuance: "building v2", Confidence: 0.85},
			{From: "sangalo", To: "unknown_node", Relation: "WORKS_ON", Confidence: 0.10}, // Should be skipped due to unresolvable 'unknwon_node' collection
		},
	}

	heatScore := 0.65

	err := writer.WriteExtractionToGraph(context.Background(), extraction, heatScore)
	assert.NoError(t, err)

	// 2 queries for nodes + 1 query for the valid edge (the second edge is unresolvable since `unknown_node` isn't in nodes list)
	assert.Len(t, mockDB.queries, 3)
	assert.Len(t, mockDB.bindVars, 3)

	// Edge query will be the 3rd query index (2)
	edgeQuery := mockDB.queries[2]
	edgeVars := mockDB.bindVars[2]

	// Verify mathematical logic in the upsert edge query
	assert.Contains(t, edgeQuery, "UPSERT { _from: @from, _to: @to, relation: @relation }")
	// Clean the query slightly from tabs for exact string assert OR use Contains
	assert.Contains(t, edgeQuery, "IN MemoryEdges")
	assert.Contains(t, edgeQuery, "confidence: (OLD.confidence + @confidence) / 2")
	assert.Contains(t, edgeQuery, "weight: OLD.weight + 1")
	assert.Contains(t, edgeQuery, "heat_score: @heat")

	// Verify path formatting and correct propagation of all fields
	assert.Equal(t, "Identities/sangalo", edgeVars["from"])
	assert.Equal(t, "Projects/openclaw", edgeVars["to"])
	assert.Equal(t, "WORKS_ON", edgeVars["relation"])
	assert.Equal(t, "building v2", edgeVars["context_nuance"]) // Ensure the nuance was included
	assert.Equal(t, 0.85, edgeVars["confidence"])
	assert.Equal(t, 0.65, edgeVars["heat"])
}

func TestWriteExtraction_Edges_InvalidRelation(t *testing.T) {
	mockDB := &mockDatabase{}
	writer := &LTMWriter{db: mockDB}

	extraction := &GraphExtraction{
		Nodes: []GraphNode{
			{ID: "sangalo", Label: "Identities", Name: "Sangalo"},
			{ID: "athena", Label: "Identities", Name: "Athena"},
		},
		Edges: []GraphEdge{
			{From: "sangalo", To: "athena", Relation: "IS_FRIENDS_WITH", ContextNuance: "talks to her every day", Confidence: 0.90}, // IS_FRIENDS_WITH is invalid
		},
	}

	heatScore := 0.70

	err := writer.WriteExtractionToGraph(context.Background(), extraction, heatScore)
	assert.NoError(t, err)

	assert.Len(t, mockDB.queries, 3) // 2 nodes + 1 edge
	assert.Len(t, mockDB.bindVars, 3)

	edgeQuery := mockDB.queries[2]
	edgeVars := mockDB.bindVars[2]

	assert.Contains(t, edgeQuery, "UPSERT { _from: @from, _to: @to, relation: @relation }")

	assert.Equal(t, "Identities/sangalo", edgeVars["from"])
	assert.Equal(t, "Identities/athena", edgeVars["to"])

	// Interceptor Verification!
	// It should have forcefully changed the relation to RELATES_TO
	assert.Equal(t, "RELATES_TO", edgeVars["relation"])
	// It should have appended the invalid relation to the context nuance
	assert.Equal(t, "talks to her every day (IS_FRIENDS_WITH)", edgeVars["context_nuance"])

	assert.Equal(t, 0.90, edgeVars["confidence"])
}
