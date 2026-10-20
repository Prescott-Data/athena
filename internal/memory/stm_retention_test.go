package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// recordingStore is an STMStorer that records the chains it is asked to form.
type recordingStore struct {
	formed [][]models.CognitiveEvent
	// during runs inside ProcessMTMFormation, to simulate events arriving meanwhile.
	during func()
}

func (r *recordingStore) CreateEmbedding(context.Context, string) (*models.EmbeddingData, error) {
	return &models.EmbeddingData{Vector: []float64{1, 0}}, nil
}
func (r *recordingStore) analyzeTopicContinuity(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (r *recordingStore) ProcessMTMFormation(_ context.Context, _, _, _ string, events []models.CognitiveEvent) error {
	r.formed = append(r.formed, events)
	if r.during != nil {
		r.during()
	}
	return nil
}
func (r *recordingStore) StoreCognitiveEvent(context.Context, *models.CognitiveEvent) (primitive.ObjectID, error) {
	return primitive.NewObjectID(), nil
}
func (r *recordingStore) SearchSimilarChains(context.Context, string, string, string, []float64, int, map[string]string) ([]*models.CognitiveChain, error) {
	return nil, nil
}

func contents(events []models.CognitiveEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Content
	}
	return out
}

func stmContents(t *testing.T, s *STMCache, tenant, user, agent string) []string {
	t.Helper()
	raw, err := s.redis.LRange(GenerateSTMKey(tenant, user, agent), 0, -1)
	require.NoError(t, err)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var e STMEvent
		require.NoError(t, json.Unmarshal([]byte(r), &e))
		out = append(out, e.Content)
	}
	return out
}

func addFacts(t *testing.T, s *STMCache, tenant, user, agent string, n int, at time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, s.AddSTMEvent(context.Background(), tenant, user, agent, STMEvent{
			Role: "agent", Type: models.STMEventTypeObservation, Content: fmt.Sprintf("fact %d", i),
			Timestamp: at,
		}))
	}
}

func TestSTM_KeepsEveryEventUntilItIsSummarised(t *testing.T) {
	const tenant, user, agent = "t-keep", "u-keep", "a"
	s := setupRealSTMCache(t, tenant, user, agent)

	addFacts(t, s, tenant, user, agent, 25, time.Now())

	assert.Len(t, stmContents(t, s, tenant, user, agent), 25, "the 15 oldest are not dropped")
	context, err := s.GetSTMContext(context.Background(), tenant, user, agent)
	require.NoError(t, err)
	assert.Len(t, context, 10, "the context window is still the newest STM_CACHE_MAX_TURNS")
}

func TestSTM_DropsAtTheRetentionLimitAreCounted(t *testing.T) {
	const tenant, user, agent = "t-limit", "u-limit", "a"
	s := setupRealSTMCache(t, tenant, user, agent)
	t.Setenv("STM_MAX_EVENTS", "5")
	before := testutil.ToFloat64(MetricSTMEventsEvicted)

	addFacts(t, s, tenant, user, agent, 8, time.Now())

	assert.Equal(t, []string{"fact 7", "fact 6", "fact 5", "fact 4", "fact 3"}, stmContents(t, s, tenant, user, agent))
	assert.Equal(t, 3.0, testutil.ToFloat64(MetricSTMEventsEvicted)-before)
}

func TestWorker_ForcedFlushRunsAtItsThresholdAndKeepsNewerEvents(t *testing.T) {
	const tenant, user, agent = "t-flush", "u-flush", "a"
	s := setupRealSTMCache(t, tenant, user, agent)
	store := &recordingStore{}
	w := NewWorker(nil, store, s.redis)

	addFacts(t, s, tenant, user, agent, 20, time.Now())
	// Two events arrive while the chain is being formed: they must survive the trim.
	store.during = func() { addFactsNamed(t, s, tenant, user, agent, "late 1", "late 2") }

	require.NoError(t, w.processCognitiveChainCheck(context.Background(),
		&models.CognitiveChainCheckTask{TenantID: tenant, UserID: user, AgentID: agent}))

	require.Len(t, store.formed, 1, "STM reached STM_MAX_EVENTS_BEFORE_FLUSH, so a chain is forced")
	assert.Equal(t, []string{"fact 0", "fact 1", "fact 2", "fact 3", "fact 4", "fact 5", "fact 6", "fact 7",
		"fact 8", "fact 9"}, contents(store.formed[0]), "the older half, oldest first")
	assert.Equal(t, []string{"late 2", "late 1", "fact 19", "fact 18", "fact 17", "fact 16", "fact 15",
		"fact 14", "fact 13", "fact 12", "fact 11", "fact 10"}, stmContents(t, s, tenant, user, agent))
}

func addFactsNamed(t *testing.T, s *STMCache, tenant, user, agent string, names ...string) {
	t.Helper()
	for _, name := range names {
		require.NoError(t, s.AddSTMEvent(context.Background(), tenant, user, agent, STMEvent{
			Role: "agent", Type: models.STMEventTypeObservation, Content: name, Timestamp: time.Now()}))
	}
}

func TestWorker_AQuietScopeIsSummarisedBeforeItExpires(t *testing.T) {
	const tenant, user, agent = "t-idle", "u-idle", "a"
	s := setupRealSTMCache(t, tenant, user, agent)
	store := &recordingStore{}
	w := NewWorker(nil, store, s.redis)
	field := STMScopeField(tenant, user, agent)
	t.Cleanup(func() { _ = s.redis.HDel(STMActivityKey, field) })
	queue := GenerateScopedQueueName(tenant, user, agent)
	t.Cleanup(func() { _ = s.redis.Delete(queue) })

	// Three facts, no topic change, far below the forced-flush threshold.
	addFacts(t, s, tenant, user, agent, 3, time.Now().Add(-40*time.Minute))
	scopes, err := s.redis.HGetAll(STMActivityKey)
	require.NoError(t, err)
	require.Contains(t, scopes, field, "the scope's activity is recorded")

	queued, err := w.SweepIdle(context.Background(), time.Now().Add(31*time.Minute))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, queued, 1)
	task, err := s.redis.RPop(queue)
	require.NoError(t, err)
	assert.True(t, strings.Contains(task, TaskTypeIdleFlush))

	require.NoError(t, w.processIdleFlush(context.Background(),
		&models.CognitiveChainCheckTask{TenantID: tenant, UserID: user, AgentID: agent}))
	require.Len(t, store.formed, 1)
	assert.Equal(t, []string{"fact 0", "fact 1", "fact 2"}, contents(store.formed[0]))
	assert.Empty(t, stmContents(t, s, tenant, user, agent))
	scopes, _ = s.redis.HGetAll(STMActivityKey)
	assert.NotContains(t, scopes, field)
}

func TestWorker_AnIdleFlushLeavesAScopeThatBecameActiveAlone(t *testing.T) {
	const tenant, user, agent = "t-active", "u-active", "a"
	s := setupRealSTMCache(t, tenant, user, agent)
	store := &recordingStore{}
	w := NewWorker(nil, store, s.redis)
	t.Cleanup(func() { _ = s.redis.HDel(STMActivityKey, STMScopeField(tenant, user, agent)) })

	addFacts(t, s, tenant, user, agent, 3, time.Now())
	require.NoError(t, w.processIdleFlush(context.Background(),
		&models.CognitiveChainCheckTask{TenantID: tenant, UserID: user, AgentID: agent}))
	assert.Empty(t, store.formed)
	assert.Len(t, stmContents(t, s, tenant, user, agent), 3)
}

func TestSTMScopeField_RoundTripsIdsWithSeparators(t *testing.T) {
	tenant, user, agent, ok := ParseSTMScopeField(STMScopeField("acme", "acme/launch#private:alice", "w:1"))
	require.True(t, ok)
	assert.Equal(t, []string{"acme", "acme/launch#private:alice", "w:1"}, []string{tenant, user, agent})
}
