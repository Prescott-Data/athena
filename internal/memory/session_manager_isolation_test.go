package memory

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// isolationDB returns a throwaway MongoDB database. Set ATHENA_TEST_MONGO_URI to run.
func isolationDB(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("ATHENA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ATHENA_TEST_MONGO_URI to run tenant isolation tests against MongoDB")
	}
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	require.NoError(t, err)
	db := client.Database(fmt.Sprintf("athena_isolation_test_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return db
}

func TestMergeCandidatesStayInTheTenant(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	now := time.Now()
	_, err := db.Collection(CognitiveChainsCollection).InsertMany(ctx, []interface{}{
		models.CognitiveChain{TenantID: "acme", UserID: "alice", ChainID: "acme-chain", Summary: "acme secret", Status: "active", StartedAt: now},
		models.CognitiveChain{TenantID: "umbrella", UserID: "alice", ChainID: "umbrella-chain", Summary: "umbrella topic", Status: "active", StartedAt: now},
		models.CognitiveChain{TenantID: "umbrella", UserID: "alice", ChainID: "umbrella-session", Summary: "", Status: "active", StartedAt: now},
	})
	require.NoError(t, err)

	sm := NewSessionManager(db, nil)
	got, err := sm.findRecentCandidates(ctx, &models.CognitiveChain{TenantID: "umbrella", UserID: "alice", ChainID: "new"})
	require.NoError(t, err)
	ids := []string{}
	for _, c := range got {
		ids = append(ids, c.ChainID)
	}
	assert.Equal(t, []string{"umbrella-chain"}, ids, "no other tenant's chains and no session records")
}

func TestCleanupOldChainsStaysInTheTenant(t *testing.T) {
	db := isolationDB(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	var docs []interface{}
	for i := 0; i < 3; i++ {
		docs = append(docs, models.CognitiveChain{TenantID: "acme", UserID: "alice", ChainID: fmt.Sprintf("acme-%d", i), Summary: "s", Status: "active", StartedAt: base.Add(time.Duration(i) * time.Minute)})
		docs = append(docs, models.CognitiveChain{TenantID: "globex", UserID: "alice", ChainID: fmt.Sprintf("globex-%d", i), Summary: "s", Status: "active", StartedAt: base.Add(-time.Duration(i) * time.Hour)})
	}
	_, err := db.Collection(CognitiveChainsCollection).InsertMany(ctx, docs)
	require.NoError(t, err)

	sm := NewSessionManager(db, nil)
	sm.config.MaxChainsPerUser = 2
	require.NoError(t, sm.CleanupOldChains(ctx, "acme", "alice"))

	cur, err := db.Collection(CognitiveChainsCollection).Find(ctx, bson.M{"status": "archived"})
	require.NoError(t, err)
	var archived []models.CognitiveChain
	require.NoError(t, cur.All(ctx, &archived))
	ids := []string{}
	for _, c := range archived {
		ids = append(ids, c.ChainID)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"acme-0"}, ids, "only acme's oldest chain beyond the cap; globex's older chains untouched")
}

func TestMergeDecisionPrefersASimilarCandidate(t *testing.T) {
	sm := &SessionManager{config: SessionConfig{SimilarityThreshold: 0.6, MergeMinConfidence: 0.7}}
	dissimilar := &ChainMergeCandidate{Chain: &models.CognitiveChain{ChainID: "unrelated"}, SimilarityScore: 0.0, MergeConfidence: 1.0}
	similar := &ChainMergeCandidate{Chain: &models.CognitiveChain{ChainID: "same-topic"}, SimilarityScore: 0.9, MergeConfidence: 0.9}

	decision, err := sm.analyzeMergeDecision(context.Background(), nil, []*ChainMergeCandidate{dissimilar, similar})
	require.NoError(t, err)
	assert.True(t, decision.ShouldMerge)
	assert.Equal(t, "same-topic", decision.TargetChain.ChainID)
}
