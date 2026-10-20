package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingGraphEraser struct {
	chains [][]string
	scopes [][2]string
}

func (g *recordingGraphEraser) EraseChains(_ context.Context, tenantID, userID string, ids []string) (int, error) {
	sorted := append([]string{tenantID, userID}, ids...)
	sort.Strings(sorted[2:])
	g.chains = append(g.chains, sorted)
	return len(ids), nil
}

func (g *recordingGraphEraser) EraseScope(_ context.Context, tenantID, userID string) (int, error) {
	g.scopes = append(g.scopes, [2]string{tenantID, userID})
	return 7, nil
}

// seedScope writes a session, a chain, two events (one with a blob) and STM for one
// tenant/user/agent, and returns the session ID.
func seedScope(t *testing.T, store *STMStore, tenant, user, agent string) string {
	t.Helper()
	ctx := context.Background()
	sid := fmt.Sprintf("s-%s-%s-%s-%d", tenant, user, agent, time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Millisecond)
	insertDocs(t, store.db.Collection(CognitiveChainsCollection),
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: sid, Kind: models.ChainKindSession, Status: "active"},
		models.CognitiveChain{TenantID: tenant, UserID: user, AgentID: agent, ChainID: "chain-" + sid, Summary: tenant + " " + user + " summary", Status: "active", SessionIDs: []string{sid}})
	insertDocs(t, store.db.Collection(CognitiveEventsCollection),
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: sid, ChainID: "chain-" + sid, Role: "user", Content: tenant + " says hi", CreatedAt: now},
		models.CognitiveEvent{TenantID: tenant, UserID: user, AgentID: agent, SessionID: sid, ChainID: sid, Role: "agent", Content: tenant + " file", BlobURI: "s3://b/" + sid, CreatedAt: now.Add(time.Second)})
	raw, _ := json.Marshal(STMEvent{Role: "user", Content: "pending", SessionID: sid, Timestamp: now})
	key := GenerateSTMKey(tenant, user, agent)
	require.NoError(t, store.redis.LPush(key, string(raw)))
	t.Cleanup(func() { _ = store.redis.Delete(key); _ = store.redis.Delete(erasedSessionKey(sid)) })
	_ = ctx
	return sid
}

func TestEraseScopeRemovesOneUserAndNothingElse(t *testing.T) {
	store, blobs := newLifecycleStore(t)
	graph := &recordingGraphEraser{}
	store.SetGraphEraser(graph)
	ctx := context.Background()
	tenant := fmt.Sprintf("acme-%d", time.Now().UnixNano())

	gone1 := seedScope(t, store, tenant, "alice", "bot")
	gone2 := seedScope(t, store, tenant, "alice", "helper")
	seedScope(t, store, tenant, "bob", "bot")            // same tenant, other user
	seedScope(t, store, tenant+"-other", "alice", "bot") // other tenant, same user ID

	report, err := store.EraseScope(ctx, tenant, "alice", "")
	require.NoError(t, err)
	assert.Equal(t, 2, report.Sessions)
	assert.Equal(t, 2, report.Chains)
	assert.Equal(t, 4, report.Events)
	assert.Equal(t, 2, report.STMEvents)
	assert.Equal(t, 2, report.Blobs)
	assert.Equal(t, 7, report.GraphEntries)
	assert.Equal(t, [][2]string{{tenant, "alice"}}, graph.scopes, "a user erasure removes the user's whole graph")
	assert.ElementsMatch(t, []string{"s3://b/" + gone1, "s3://b/" + gone2}, blobs.deleted)

	for _, id := range []string{gone1, gone2} {
		_, err := store.SessionRecord(ctx, id)
		assert.ErrorIs(t, err, ErrSessionNotFound)
		tomb, _ := store.redis.Exists(erasedSessionKey(id))
		assert.True(t, tomb, "chain formations already running must drop the erased sessions' events")
	}
	n, _ := store.redis.LLen(GenerateSTMKey(tenant, "alice", "bot"))
	assert.Zero(t, n)
	n, _ = store.redis.LLen(GenerateSTMKey(tenant, "bob", "bot"))
	assert.Equal(t, int64(1), n, "another user's STM stays")
	n, _ = store.redis.LLen(GenerateSTMKey(tenant+"-other", "alice", "bot"))
	assert.Equal(t, int64(1), n, "another tenant's user with the same ID stays")

	assert.ElementsMatch(t, []string{tenant + "-other alice summary", tenant + " bob summary"}, summariesIn(t, store))
}

func TestEraseScopeOfOneAgentErasesOnlyItsGraphContributions(t *testing.T) {
	store, _ := newLifecycleStore(t)
	graph := &recordingGraphEraser{}
	store.SetGraphEraser(graph)
	tenant := fmt.Sprintf("acme-%d", time.Now().UnixNano())
	gone := seedScope(t, store, tenant, "alice", "bot")
	seedScope(t, store, tenant, "alice", "helper")

	report, err := store.EraseScope(context.Background(), tenant, "alice", "bot")
	require.NoError(t, err)
	assert.Equal(t, 1, report.Sessions)
	assert.Empty(t, graph.scopes, "the user's graph is shared by their agents")
	assert.Equal(t, [][]string{{tenant, "alice", "chain-" + gone}}, graph.chains)
	assert.ElementsMatch(t, []string{tenant + " alice summary"}, summariesIn(t, store))
}

func TestEraseScopeOfAWholeTenant(t *testing.T) {
	store, _ := newLifecycleStore(t)
	tenant := fmt.Sprintf("acme-%d", time.Now().UnixNano())
	seedScope(t, store, tenant, "alice", "bot")
	seedScope(t, store, tenant, "bob", "bot")
	seedScope(t, store, tenant+"x", "carol", "bot")

	report, err := store.EraseScope(context.Background(), tenant, "", "")
	require.NoError(t, err)
	assert.Equal(t, 2, report.Sessions)
	assert.ElementsMatch(t, []string{tenant + "x carol summary"}, summariesIn(t, store))

	again, err := store.EraseScope(context.Background(), tenant, "", "")
	require.NoError(t, err, "erasing an empty scope is not an error, so a retry is safe")
	assert.Zero(t, again.Sessions)
}

func TestEraseSessionErasesItsChainsFromTheGraph(t *testing.T) {
	store, _ := newLifecycleStore(t)
	graph := &recordingGraphEraser{}
	store.SetGraphEraser(graph)
	tenant := fmt.Sprintf("acme-%d", time.Now().UnixNano())
	sid := seedScope(t, store, tenant, "alice", "bot")

	record, err := store.SessionRecord(context.Background(), sid)
	require.NoError(t, err)
	report, err := store.EraseSession(context.Background(), record)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{tenant, "alice", "chain-" + sid}}, graph.chains)
	assert.Equal(t, 1, report.GraphEntries)
}

func summariesIn(t *testing.T, store *STMStore) []string {
	t.Helper()
	cursor, err := store.db.Collection(CognitiveChainsCollection).Find(context.Background(), map[string]interface{}{"summary": map[string]interface{}{"$ne": ""}})
	require.NoError(t, err)
	var chains []models.CognitiveChain
	require.NoError(t, cursor.All(context.Background(), &chains))
	out := []string{}
	for _, c := range chains {
		out = append(out, c.Summary)
	}
	return out
}

func TestEraseScopeOfAnAgentFollowsChainsItsSessionsMergedInto(t *testing.T) {
	store, _ := newLifecycleStore(t)
	graph := &recordingGraphEraser{}
	store.SetGraphEraser(graph)
	tenant := fmt.Sprintf("acme-%d", time.Now().UnixNano())
	gone := seedScope(t, store, tenant, "alice", "bot")
	kept := seedScope(t, store, tenant, "alice", "helper")
	// bot's conversation was merged into a chain that kept helper's agent ID.
	insertDocs(t, store.db.Collection(CognitiveChainsCollection),
		models.CognitiveChain{TenantID: tenant, UserID: "alice", AgentID: "helper", ChainID: "merged", Summary: "helper and bot talk",
			Status: "active", SessionIDs: []string{kept, gone}})

	_, err := store.EraseScope(context.Background(), tenant, "alice", "bot")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{tenant + " alice summary"}, summariesIn(t, store),
		"the merged chain holds bot's conversation and goes; helper's own chain stays")
	require.Len(t, graph.chains, 1)
	assert.ElementsMatch(t, []string{"chain-" + gone, "merged"}, graph.chains[0][2:])
}
