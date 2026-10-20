package memory

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/arangodb/go-driver"
	arangohttp "github.com/arangodb/go-driver/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// arangoWriter returns an LTMWriter on a throwaway ArangoDB database. Set
// ATHENA_TEST_ARANGO_URL (and ATHENA_TEST_ARANGO_PASSWORD) to run.
func arangoWriter(t *testing.T) *LTMWriter {
	t.Helper()
	url := os.Getenv("ATHENA_TEST_ARANGO_URL")
	if url == "" {
		t.Skip("set ATHENA_TEST_ARANGO_URL to run LTM provenance tests against ArangoDB")
	}
	pass := os.Getenv("ATHENA_TEST_ARANGO_PASSWORD")
	ctx := context.Background()
	name := fmt.Sprintf("ltm_test_%d", time.Now().UnixNano())
	conn, err := arangohttp.NewConnection(arangohttp.ConnectionConfig{Endpoints: []string{url}})
	require.NoError(t, err)
	client, err := driver.NewClient(driver.ClientConfig{Connection: conn, Authentication: driver.BasicAuthentication("root", pass)})
	require.NoError(t, err)
	db, err := client.CreateDatabase(ctx, name, nil)
	require.NoError(t, err)
	for _, col := range ltmCollections {
		typ := driver.CollectionTypeDocument
		if col == "MemoryEdges" {
			typ = driver.CollectionTypeEdge
		}
		_, err := db.CreateCollection(ctx, col, &driver.CreateCollectionOptions{Type: typ})
		require.NoError(t, err)
	}
	w, err := NewLTMWriter(ctx, url, "root", pass, name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.db.Remove(context.Background())
	})
	return w
}

func names(t *testing.T, w *LTMWriter, tenant, user string) []string {
	t.Helper()
	cursor, err := w.db.Query(context.Background(), `
		FOR d IN UNION((FOR x IN Identities RETURN x), (FOR x IN Concepts RETURN x), (FOR x IN Tools RETURN x), (FOR x IN Projects RETURN x))
			FILTER d.tenant_id == @t AND d.user_id == @u RETURN d.name`, map[string]interface{}{"t": tenant, "u": user})
	require.NoError(t, err)
	defer cursor.Close()
	var out []string
	for {
		var n string
		if _, err := cursor.ReadDocument(context.Background(), &n); driver.IsNoMoreDocuments(err) {
			break
		} else {
			require.NoError(t, err)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func edge(t *testing.T, w *LTMWriter, tenant, user string) map[string]interface{} {
	t.Helper()
	cursor, err := w.db.Query(context.Background(), `FOR e IN MemoryEdges FILTER e.tenant_id == @t AND e.user_id == @u RETURN e`,
		map[string]interface{}{"t": tenant, "u": user})
	require.NoError(t, err)
	defer cursor.Close()
	var e map[string]interface{}
	if _, err := cursor.ReadDocument(context.Background(), &e); driver.IsNoMoreDocuments(err) {
		return nil
	}
	return e
}

var samTeam = &GraphExtraction{
	Nodes: []GraphNode{{ID: "sam", Label: "Identities", Name: "Sam"}, {ID: "support", Label: "Projects", Name: "Support"}},
	Edges: []GraphEdge{{From: "sam", To: "support", Relation: "WORKS_ON", Confidence: 0.9}},
}

func TestLTMProvenanceAgainstArango(t *testing.T) {
	w := arangoWriter(t)
	ctx := context.Background()

	require.NoError(t, w.WriteExtractionToGraph(ctx, "acme", "alice", "chain-a", samTeam, 0.5))
	require.NoError(t, w.WriteExtractionToGraph(ctx, "acme", "alice", "chain-a", samTeam, 0.5))
	assert.Equal(t, float64(1), edge(t, w, "acme", "alice")["weight"], "promoting the same chain again is not new evidence")

	require.NoError(t, w.WriteExtractionToGraph(ctx, "acme", "alice", "chain-b", samTeam, 0.5))
	assert.Equal(t, float64(2), edge(t, w, "acme", "alice")["weight"])
	require.NoError(t, w.WriteExtractionToGraph(ctx, "globex", "alice", "chain-g", samTeam, 0.5))

	// Erasing one of two supporting chains keeps the knowledge, with less weight.
	n, err := w.EraseChains(ctx, "acme", "alice", []string{"chain-a"})
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, []string{"Sam", "Support"}, names(t, w, "acme", "alice"))
	assert.Equal(t, float64(1), edge(t, w, "acme", "alice")["weight"])

	// Erasing the last one removes it, and nothing of the other tenant's.
	n, err = w.EraseChains(ctx, "acme", "alice", []string{"chain-b"})
	require.NoError(t, err)
	assert.Equal(t, 3, n, "two nodes and one edge")
	assert.Empty(t, names(t, w, "acme", "alice"))
	assert.Nil(t, edge(t, w, "acme", "alice"))
	assert.Equal(t, []string{"Sam", "Support"}, names(t, w, "globex", "alice"))

	// Scope erasure removes the whole graph of a user.
	require.NoError(t, w.WriteExtractionToGraph(ctx, "globex", "bob", "chain-x", samTeam, 0.5))
	n, err = w.EraseScope(ctx, "globex", "alice")
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Empty(t, names(t, w, "globex", "alice"))
	assert.Equal(t, []string{"Sam", "Support"}, names(t, w, "globex", "bob"))
	n, err = w.EraseScope(ctx, "globex", "")
	require.NoError(t, err)
	assert.Equal(t, 3, n, "a tenant erasure takes every user")
}

func TestEntriesFromBeforeProvenanceSurviveErasureOfNewerChains(t *testing.T) {
	w := arangoWriter(t)
	ctx := context.Background()
	key := ltmScopedKey("acme", "alice", "sam")
	_, err := w.db.Query(ctx, `INSERT { _key: @k, entity_id: "sam", tenant_id: "acme", user_id: "alice", name: "Sam" } INTO Identities`,
		map[string]interface{}{"k": key})
	require.NoError(t, err)

	require.NoError(t, w.WriteExtractionToGraph(ctx, "acme", "alice", "chain-new",
		&GraphExtraction{Nodes: []GraphNode{{ID: "sam", Label: "Identities", Name: "Sam"}}}, 0.5))
	_, err = w.EraseChains(ctx, "acme", "alice", []string{"chain-new"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Sam"}, names(t, w, "acme", "alice"), "older support for the entry is unknown, so it stays")
}
