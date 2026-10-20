package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// slowFormingStore records each chain it forms, slowly, as an LLM summary would be.
type slowFormingStore struct {
	mu     sync.Mutex
	formed [][]string
}

func (s *slowFormingStore) CreateEmbedding(context.Context, string) (*models.EmbeddingData, error) {
	return &models.EmbeddingData{Vector: []float64{1, 0}}, nil
}
func (s *slowFormingStore) analyzeTopicContinuity(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (s *slowFormingStore) ProcessMTMFormation(_ context.Context, _, _, _ string, events []models.CognitiveEvent) error {
	time.Sleep(300 * time.Millisecond)
	contents := make([]string, len(events))
	for i, e := range events {
		contents[i] = e.Content
	}
	s.mu.Lock()
	s.formed = append(s.formed, contents)
	s.mu.Unlock()
	return nil
}
func (s *slowFormingStore) StoreCognitiveEvent(context.Context, *models.CognitiveEvent) (primitive.ObjectID, error) {
	return primitive.NewObjectID(), nil
}
func (s *slowFormingStore) SearchSimilarChains(context.Context, string, string, string, []float64, int, float64, map[string]string) ([]*models.CognitiveChain, error) {
	return nil, nil
}

type noopTaskQueue struct{}

func (noopTaskQueue) GetQueueStats(context.Context) (map[string]interface{}, error) { return nil, nil }
func (noopTaskQueue) MarkTaskResult(context.Context, string, string, string, string, bool, string) error {
	return nil
}

func TestWorker_TwoWorkersNeverSummariseTheSameEventsTwice(t *testing.T) {
	client, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: real Redis not available: %v", err)
	}
	const tenant, user, agent = "t-dup", "u-dup", "a"
	key := GenerateSTMKey(tenant, user, agent)
	queue := GenerateScopedQueueName(tenant, user, agent)
	cleanup := func() {
		_ = client.Delete(key)
		_ = client.Delete(queue)
		_ = client.Delete(GlobalWorkQueueName)
		_ = client.Delete("stm_scope_lock:v1:" + queue)
	}
	cleanup()
	t.Cleanup(cleanup)

	// 20 events: the forced-flush threshold, so a chain check forms a chain.
	for i := 0; i < 20; i++ {
		raw, _ := json.Marshal(STMEvent{Role: "agent", Type: models.STMEventTypeObservation,
			Content: fmt.Sprintf("fact %d", i), Timestamp: time.Now()})
		require.NoError(t, client.LPush(key, string(raw)))
	}
	// Two chain-check tasks for the scope, as two stored events would enqueue.
	for i := 0; i < 2; i++ {
		raw, _ := json.Marshal(models.CognitiveChainCheckTask{ID: fmt.Sprint(i), Type: TaskTypeCognitiveChainCheck,
			TenantID: tenant, UserID: user, AgentID: agent})
		envelope, _ := json.Marshal(TaskEnvelope{ID: fmt.Sprint(i), Type: TaskTypeCognitiveChainCheck, Payload: raw})
		require.NoError(t, client.LPush(queue, string(envelope)))
		require.NoError(t, client.LPush(GlobalWorkQueueName, queue))
	}

	store := &slowFormingStore{}
	w := NewWorker(noopTaskQueue{}, store, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ctx.Err() == nil {
				if remaining, _ := client.LLen(GlobalWorkQueueName); remaining == 0 {
					return
				}
				_ = w.processNextTask(ctx, id)
			}
		}(i)
	}
	wg.Wait()

	require.Len(t, store.formed, 1, "the events were summarised once, not once per worker")
	assert.Equal(t, "fact 0", store.formed[0][0], "the oldest half, oldest first")
}
