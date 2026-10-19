package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// recordingBlobStore records deleted URIs.
type recordingBlobStore struct{ deleted []string }

func (b *recordingBlobStore) Upload(context.Context, string, io.Reader, string) (string, error) {
	return "", nil
}
func (b *recordingBlobStore) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}
func (b *recordingBlobStore) Delete(_ context.Context, uri string) error {
	b.deleted = append(b.deleted, uri)
	return nil
}

// newLifecycleStore returns an STMStore on a throwaway MongoDB database and the
// real test Redis. Set ATHENA_TEST_MONGO_URI to run these tests.
func newLifecycleStore(t *testing.T) (*STMStore, *recordingBlobStore) {
	t.Helper()
	uri := os.Getenv("ATHENA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ATHENA_TEST_MONGO_URI to run session lifecycle tests against MongoDB")
	}
	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	db := client.Database(fmt.Sprintf("athena_lifecycle_test_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
		redisClient.Close()
	})
	blobs := &recordingBlobStore{}
	return &STMStore{db: db, redis: redisClient, blobStore: blobs}, blobs
}

func insertDocs(t *testing.T, col *mongo.Collection, docs ...interface{}) {
	t.Helper()
	_, err := col.InsertMany(context.Background(), docs)
	require.NoError(t, err)
}

func chainIDsIn(t *testing.T, store *STMStore) []string {
	t.Helper()
	cursor, err := store.db.Collection(CognitiveChainsCollection).Find(context.Background(), bson.M{})
	require.NoError(t, err)
	var chains []models.CognitiveChain
	require.NoError(t, cursor.All(context.Background(), &chains))
	ids := make([]string, 0, len(chains))
	for _, c := range chains {
		ids = append(ids, c.ChainID)
	}
	sort.Strings(ids)
	return ids
}

func eventContentsIn(t *testing.T, store *STMStore) []string {
	t.Helper()
	cursor, err := store.db.Collection(CognitiveEventsCollection).Find(context.Background(), bson.M{})
	require.NoError(t, err)
	var events []models.CognitiveEvent
	require.NoError(t, cursor.All(context.Background(), &events))
	contents := make([]string, 0, len(events))
	for _, e := range events {
		contents = append(contents, e.Content)
	}
	sort.Strings(contents)
	return contents
}

func TestEraseSessionRemovesOnlyThatSessionsMemory(t *testing.T) {
	store, blobs := newLifecycleStore(t)
	ctx := context.Background()
	tenant, user, agent := "t-erase", fmt.Sprintf("u-%d", time.Now().UnixNano()), "a"
	s1, s2 := "session-one-"+user, "session-two-"+user
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }

	chains := store.db.Collection(CognitiveChainsCollection)
	events := store.db.Collection(CognitiveEventsCollection)
	insertDocs(t, chains,
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: s1, Status: "active", StartedAt: at(0)},
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: s2, Status: "active", StartedAt: at(0)},
		// Formed from both sessions: it summarises s1's content, so it must go.
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-shared", Summary: "both", Status: "active", SessionIDs: []string{s1, s2}},
		// Formed from s2 only: it must stay.
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-s2", Summary: "s2 only", Status: "active", SessionIDs: []string{s2}},
		// Formed by an older release: no provenance, linked to s1 only by its span.
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-legacy", Summary: "legacy", Status: "active", StartedAt: at(5), LastEventAt: at(6)},
		// Older release, later merged with a chain of s2 (so it has sessionIds),
		// still covering s1's legacy event: it holds s1's content and must go.
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-legacy-merged", Summary: "legacy; s2", Status: "active", StartedAt: at(4).Add(30 * time.Second), LastEventAt: at(10), SessionIDs: []string{s2}},
		// Older release, same scope, but its span covers none of s1's events.
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-legacy-other", Summary: "other", Status: "active", StartedAt: at(20), LastEventAt: at(21)},
	)
	insertDocs(t, events,
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: s1, ChainID: "chain-shared", Role: "user", Content: "s1 first", Status: "in_mtm", CreatedAt: at(1)},
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: s1, ChainID: s1, Role: "agent", Content: "s1 blob", BlobURI: "s3://bucket/s1", Status: "in_stm", CreatedAt: at(2)},
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: s2, ChainID: "chain-shared", Role: "user", Content: "s2 shared", Status: "in_mtm", CreatedAt: at(3)},
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: s2, ChainID: "chain-s2", Role: "user", Content: "s2 own", Status: "in_mtm", CreatedAt: at(4)},
		// Older release: the durable original keeps chainId == session ID and never
		// gets promoted; the copy made at formation has no tenant and no session.
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, ChainID: s1, Role: "user", Content: "s1 legacy", Status: "in_stm", CreatedAt: at(5)},
		models.CognitiveEvent{Role: "user", Content: "s1 legacy", Status: "in_mtm", CreatedAt: at(5)},
	)

	key := GenerateSTMKey(tenant, user, agent)
	t.Cleanup(func() { _ = store.redis.Delete(key) })
	for _, e := range []STMEvent{
		{Role: "user", Content: "s2 pending", SessionID: s2, ChainID: s2, Timestamp: at(9)},
		{Role: "user", Content: "s1 pending", SessionID: s1, ChainID: s1, Timestamp: at(8)},
		{Role: "user", Content: "s1 interaction before sessionId", ChainID: s1, Timestamp: at(7)},
		{Role: "user", Content: "s1 legacy", Timestamp: at(5)}, // legacy StoreEvent entry: no IDs at all
	} {
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		require.NoError(t, store.redis.LPush(key, string(raw)))
	}

	record, err := store.SessionRecord(ctx, s1)
	require.NoError(t, err)
	report, err := store.EraseSession(ctx, record)
	require.NoError(t, err)

	assert.Equal(t, 3, report.STMEvents)
	assert.Equal(t, 4, report.Events, "two current events, the legacy original and its copy")
	assert.Equal(t, 3, report.Chains, "the shared chain and both legacy chains")
	assert.Equal(t, []string{"s3://bucket/s1"}, blobs.deleted)

	assert.Equal(t, []string{"chain-legacy-other", "chain-s2", s2}, chainIDsIn(t, store))
	assert.Equal(t, []string{"s2 own", "s2 shared"}, eventContentsIn(t, store))

	remaining, err := store.redis.LRange(key, 0, -1)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Contains(t, remaining[0], "s2 pending")

	_, err = store.SessionRecord(ctx, s1)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	tombstoned, err := store.redis.Exists(erasedSessionKey(s1))
	require.NoError(t, err)
	assert.True(t, tombstoned)
	_ = store.redis.Delete(erasedSessionKey(s1))
}

func TestFormationDropsEventsOfErasedSessions(t *testing.T) {
	store, _ := newLifecycleStore(t)
	gone := fmt.Sprintf("erased-%d", time.Now().UnixNano())
	require.NoError(t, store.redis.SetEX(erasedSessionKey(gone), "1", time.Minute))
	t.Cleanup(func() { _ = store.redis.Delete(erasedSessionKey(gone)) })

	kept := store.dropErasedSessionEvents([]models.CognitiveEvent{
		{SessionID: gone, Content: "a"},
		{SessionID: "live", Content: "b"},
		{Content: "c"},
	})
	contents := []string{}
	for _, e := range kept {
		contents = append(contents, e.Content)
	}
	assert.Equal(t, []string{"b", "c"}, contents)
	assert.True(t, store.chainHasErasedSession(&models.CognitiveChain{SessionIDs: []string{"live", gone}}))
	assert.False(t, store.chainHasErasedSession(&models.CognitiveChain{SessionIDs: []string{"live"}}))
}

func TestPromoteEventsToChainUpdatesStoredCopiesInPlace(t *testing.T) {
	store, _ := newLifecycleStore(t)
	ctx := context.Background()
	stored := models.CognitiveEvent{TenantID: "t", UserID: "u", AgentID: "a", SessionID: "s", ChainID: "s", Role: "user", Content: "stored", Status: "in_stm"}
	id, err := store.StoreCognitiveEvent(ctx, &stored)
	require.NoError(t, err)

	batch := []models.CognitiveEvent{
		{ID: id, TenantID: "t", UserID: "u", AgentID: "a", SessionID: "s", Role: "user", Content: "stored", EventIndex: 0},
		{TenantID: "t", UserID: "u", AgentID: "a", SessionID: "s", Role: "agent", Content: "never stored", EventIndex: 1},
	}
	require.NoError(t, store.promoteEventsToChain(ctx, "chain-1", batch))
	assert.False(t, batch[1].ID.IsZero(), "inserted events get an ID for vector indexing")

	cursor, err := store.db.Collection(CognitiveEventsCollection).Find(ctx, bson.M{})
	require.NoError(t, err)
	var all []models.CognitiveEvent
	require.NoError(t, cursor.All(ctx, &all))
	require.Len(t, all, 2, "the stored copy is promoted, not duplicated")
	for _, e := range all {
		assert.Equal(t, "in_mtm", e.Status)
		assert.Equal(t, "t", e.TenantID)
		assert.Equal(t, "s", e.SessionID)
		if e.ID == id {
			assert.Equal(t, "chain-1", e.ChainID)
		}
	}
}

func TestSessionRecordNotFound(t *testing.T) {
	store, _ := newLifecycleStore(t)
	_, err := store.SessionRecord(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrSessionNotFound)
	_, err = store.SessionRecord(context.Background(), "")
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestCognitiveEventFromSTMKeepsIdentity(t *testing.T) {
	id := primitive.NewObjectID()
	evt := cognitiveEventFromSTM(STMEvent{Role: "user", Content: "x", SessionID: "s", ChainID: "s", EventID: id.Hex(), BlobURI: "s3://b/k"})
	assert.Equal(t, id, evt.ID)
	assert.Equal(t, "s", evt.SessionID)
	assert.Equal(t, "s3://b/k", evt.BlobURI)
	assert.Empty(t, evt.ChainID, "chain formation assigns the chain")

	legacy := cognitiveEventFromSTM(STMEvent{Role: "user", Content: "x", EventID: "not-hex"})
	assert.True(t, legacy.ID.IsZero())
}

type failingBlobStore struct{ recordingBlobStore }

func (b *failingBlobStore) Delete(context.Context, string) error { return fmt.Errorf("storage down") }

func TestEraseSessionFailureLeavesSessionIntactAndRetryable(t *testing.T) {
	store, _ := newLifecycleStore(t)
	store.blobStore = &failingBlobStore{}
	ctx := context.Background()
	sid := fmt.Sprintf("s-fail-%d", time.Now().UnixNano())
	insertDocs(t, store.db.Collection(CognitiveChainsCollection),
		models.CognitiveChain{TenantID: "t", UserID: "u", AgentID: "a", ChainID: sid, Status: "active"})
	insertDocs(t, store.db.Collection(CognitiveEventsCollection),
		models.CognitiveEvent{TenantID: "t", SessionID: sid, ChainID: sid, Content: "payload", BlobURI: "s3://b/k", CreatedAt: time.Now()})

	record, err := store.SessionRecord(ctx, sid)
	require.NoError(t, err)
	_, err = store.EraseSession(ctx, record)
	require.Error(t, err)

	_, err = store.SessionRecord(ctx, sid)
	assert.NoError(t, err, "the session survives a failed erasure")
	assert.Equal(t, []string{"payload"}, eventContentsIn(t, store))
	tombstoned, err := store.redis.Exists(erasedSessionKey(sid))
	require.NoError(t, err)
	assert.False(t, tombstoned, "a failed erasure does not pause the session's memory")

	store.blobStore = &recordingBlobStore{}
	_, err = store.EraseSession(ctx, record)
	require.NoError(t, err, "retry succeeds")
	assert.Empty(t, eventContentsIn(t, store))
	_ = store.redis.Delete(erasedSessionKey(sid))
}

func TestSessionRecordRejectsChainIDs(t *testing.T) {
	store, _ := newLifecycleStore(t)
	ctx := context.Background()
	insertDocs(t, store.db.Collection(CognitiveChainsCollection),
		models.CognitiveChain{ChainID: "marked", Kind: models.ChainKindSession, TenantID: "t"},
		models.CognitiveChain{ChainID: "legacy-session", TenantID: "t"},
		models.CognitiveChain{ChainID: "mtm-chain", Summary: "a summary", SessionIDs: []string{"marked"}},
		models.CognitiveChain{ChainID: "legacy-mtm-chain", Summary: "a summary"},
	)
	for _, id := range []string{"marked", "legacy-session"} {
		_, err := store.SessionRecord(ctx, id)
		assert.NoError(t, err, id)
	}
	for _, id := range []string{"mtm-chain", "legacy-mtm-chain"} {
		_, err := store.SessionRecord(ctx, id)
		assert.ErrorIs(t, err, ErrSessionNotFound, "a segment ID must not resolve to a session: %s", id)
	}
}

func TestWorkerRemovesFormedEntriesByValueWhenListShifts(t *testing.T) {
	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer redisClient.Close()
	key := fmt.Sprintf("stm_test_remove_formed_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = redisClient.Delete(key) })
	for _, v := range []string{"old-1", "old-2", "new-1", "erased", "new-2"} {
		require.NoError(t, redisClient.LPush(key, v)) // list: new-2, erased, new-1, old-2, old-1
	}
	// While a formation of old-1/old-2 runs, a session deletion removes an entry and
	// a new event arrives; index positions read before the formation are now stale.
	require.NoError(t, redisClient.LRem(key, 1, "erased"))
	require.NoError(t, redisClient.LPush(key, "new-3"))

	w := &Worker{redis: redisClient}
	w.removeFormedEntries(key, []string{"old-2", "old-1"})

	remaining, err := redisClient.LRange(key, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"new-3", "new-2", "new-1"}, remaining)
}
