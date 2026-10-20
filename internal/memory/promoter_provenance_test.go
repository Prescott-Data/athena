package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type fakeExtractor struct{ calls int }

func (f *fakeExtractor) ExtractGraphFromSummary(context.Context, string) (*GraphExtraction, error) {
	f.calls++
	return &GraphExtraction{Nodes: []GraphNode{{ID: "x", Label: "Concepts", Name: "X"}}}, nil
}

// erasingWriter records writes and erasures; eraseDuring deletes the chain document
// while it is being written, as a concurrent erasure would.
type erasingWriter struct {
	db          *mongo.Database
	written     []string
	erased      []string
	eraseDuring bool
	markDuring  bool
	fail        bool
}

func (w *erasingWriter) WriteExtractionToGraph(ctx context.Context, _, _, chainID string, _ *GraphExtraction, _ float64) error {
	w.written = append(w.written, chainID)
	if w.eraseDuring {
		_, _ = w.db.Collection(CognitiveChainsCollection).DeleteOne(ctx, bson.M{"chainId": chainID})
	}
	if w.markDuring {
		_, _ = w.db.Collection(CognitiveChainsCollection).UpdateOne(ctx, bson.M{"chainId": chainID}, bson.M{"$set": bson.M{chainErasing: true}})
	}
	if w.fail {
		return fmt.Errorf("arangodb unavailable")
	}
	return nil
}
func (w *erasingWriter) EraseChains(_ context.Context, _, _ string, ids []string) (int, error) {
	w.erased = append(w.erased, ids...)
	return len(ids), nil
}
func (w *erasingWriter) EraseScope(context.Context, string, string) (int, error) { return 0, nil }

func hotChain(id, summary, promoted string) models.CognitiveChain {
	now := time.Now()
	return models.CognitiveChain{TenantID: "acme", UserID: "alice", ChainID: id, Summary: summary, Status: "active",
		EventCount: 4, IntrinsicImportance: 1, RecallStrength: 1, StartedAt: now, LastEventAt: now, LTMPromotedSummary: promoted}
}

func TestPromoterSkipsChainsAlreadyPromotedUnchanged(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	_, err := db.Collection(CognitiveChainsCollection).InsertMany(ctx, []interface{}{
		hotChain("done", "same summary", summaryHash("same summary")),
		hotChain("changed", "merged summary", summaryHash("old summary")),
		hotChain("new", "fresh summary", ""),
	})
	require.NoError(t, err)
	extractor, writer := &fakeExtractor{}, &erasingWriter{db: db}
	p := NewPromoter(db, extractor, writer)

	require.NoError(t, p.RunOnce(ctx, 0))
	assert.ElementsMatch(t, []string{"changed", "new"}, writer.written)

	require.NoError(t, p.RunOnce(ctx, 0))
	assert.Len(t, writer.written, 2, "a second run promotes nothing: summaries are unchanged")
	assert.Equal(t, 2, extractor.calls, "no LLM extraction for already-promoted chains")
}

func TestPromotionOfAnErasedChainIsUndone(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	_, err := db.Collection(CognitiveChainsCollection).InsertOne(ctx, hotChain("gone", "summary", ""))
	require.NoError(t, err)
	writer := &erasingWriter{db: db, eraseDuring: true}

	require.NoError(t, NewPromoter(db, &fakeExtractor{}, writer).RunOnce(ctx, 0))
	assert.Equal(t, []string{"gone"}, writer.erased)
}

func TestPromotionRacingAnErasureIsUndone(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	_, err := db.Collection(CognitiveChainsCollection).InsertOne(ctx, hotChain("erasing", "summary", ""))
	require.NoError(t, err)
	writer := &erasingWriter{db: db, markDuring: true}

	require.NoError(t, NewPromoter(db, &fakeExtractor{}, writer).RunOnce(ctx, 0))
	assert.Equal(t, []string{"erasing"}, writer.erased, "the erasure marked the chain while it was written")

	writer.written = nil
	require.NoError(t, NewPromoter(db, &fakeExtractor{}, writer).RunOnce(ctx, 0))
	assert.Empty(t, writer.written, "a chain being erased is not promoted")
}

func TestAFailedGraphWriteIsRetried(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	_, err := db.Collection(CognitiveChainsCollection).InsertOne(ctx, hotChain("flaky", "summary", ""))
	require.NoError(t, err)
	writer := &erasingWriter{db: db, fail: true}
	p := NewPromoter(db, &fakeExtractor{}, writer)

	require.NoError(t, p.RunOnce(ctx, 0))
	writer.fail = false
	require.NoError(t, p.RunOnce(ctx, 0))
	assert.Equal(t, []string{"flaky", "flaky"}, writer.written, "not recorded as promoted after a failed write")
}
