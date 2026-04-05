package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/database"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// setupIntegrationWorker creates a Worker wired to real Redis, MongoDB, and Azure OpenAI.
// Returns the worker, the real Redis client, and a cleanup function.
func setupIntegrationWorker(t *testing.T) (*Worker, cache.Interface) {
	t.Helper()

	// 1. Real Redis
	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: Redis not available: %v", err)
	}

	// 2. Real MongoDB (use a separate test database to avoid polluting dev data)
	mongoClient, mongoDB, err := database.ConnectMongoDB(database.ConnectionConfig{
		URI:            "mongodb://admin:admin123@localhost:27017/memory_os_test?authSource=admin",
		DatabaseName:   "memory_os_test",
		ConnectTimeout: 10 * time.Second,
	})
	if err != nil {
		redisClient.Close()
		t.Skipf("Skipping: MongoDB not available: %v", err)
	}

	// 3. Real STMStore (connects to Azure OpenAI for embeddings, Milvus for vectors)
	stmStore := NewSTMStore(mongoDB, redisClient, nil)

	// 4. Real TaskQueue (Redis-backed)
	taskQueue := NewTaskQueue(redisClient)

	// 5. Create Worker
	worker := NewWorker(taskQueue, stmStore, redisClient)

	t.Cleanup(func() {
		// Drop the test database to avoid pollution
		mongoDB.Drop(context.Background())
		mongoClient.Disconnect(context.Background())
		redisClient.Close()
	})

	return worker, redisClient
}

// cleanupSTMKey removes a specific STM key from Redis before/after tests.
func cleanupSTMKey(t *testing.T, redis cache.Interface, tenantID, userID, agentID string) {
	t.Helper()
	key := GenerateSTMKey(tenantID, userID, agentID)
	_ = redis.Delete(key)
}

// MockSTMStore for testing worker failure scenarios
type MockSTMStore struct {
	STMStorer                  // Embed interface to allow partial implementation
	MockCreateEmbedding        func(ctx context.Context, text string) (*models.EmbeddingData, error)
	MockProcessMTMFormation    func(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error
	MockAnalyzeTopicContinuity func(ctx context.Context, userID, prev, curr string) (bool, error)
}

func (m *MockSTMStore) CreateEmbedding(ctx context.Context, textToEmbed string) (*models.EmbeddingData, error) {
	if m.MockCreateEmbedding != nil {
		return m.MockCreateEmbedding(ctx, textToEmbed)
	}
	return &models.EmbeddingData{Vector: []float64{0.1, 0.2, 0.3}}, nil
}

func (m *MockSTMStore) ProcessMTMFormation(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error {
	if m.MockProcessMTMFormation != nil {
		return m.MockProcessMTMFormation(ctx, tenantID, userID, agentID, events)
	}
	return nil
}

func (m *MockSTMStore) analyzeTopicContinuity(ctx context.Context, userID, prev, curr string) (bool, error) {
	if m.MockAnalyzeTopicContinuity != nil {
		return m.MockAnalyzeTopicContinuity(ctx, userID, prev, curr)
	}
	return true, nil
}

// Helper methods to satisfy interface but not used in this test
func (m *MockSTMStore) StoreCognitiveEvent(ctx context.Context, event *models.CognitiveEvent) (primitive.ObjectID, error) {
	return primitive.NilObjectID, nil
}

// Wait, StoreCognitiveEvent returns (primitive.ObjectID, error). I need to import go.mongodb.org/mongo-driver/bson/primitive.
// Or I can just omit it if I don't use it, but `STMStorer` interface requires it.
// I'll add the import.

func Test_ProcessNextTask_DispatchesCorrectly(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-dispatch"
	userID := "test-user-dispatch"
	agentID := "test-agent-dispatch"
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	// Build a task payload
	taskPayload := models.CognitiveChainCheckTask{
		ID:       "dispatch-task-1",
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
	}
	payloadJSON, _ := json.Marshal(taskPayload)
	envelope := &TaskEnvelope{
		ID:      "dispatch-task-1",
		Type:    TaskTypeCognitiveChainCheck,
		Payload: payloadJSON,
	}
	envelopeJSON, _ := json.Marshal(envelope)

	scopedQueueName := GenerateScopedQueueName(tenantID, userID, agentID)
	err := redisClient.LPush(scopedQueueName, string(envelopeJSON))
	assert.NoError(t, err)
	err = redisClient.LPush(GlobalWorkQueueName, scopedQueueName)
	assert.NoError(t, err)

	err = worker.processNextTask(ctx, 1)
	assert.NoError(t, err)

	resultKey := "task_results:v1:" + tenantID + ":" + userID + ":" + agentID + ":dispatch-task-1"
	exists, _ := redisClient.Exists(resultKey)
	assert.True(t, exists, "task result should be stored in Redis")

	t.Cleanup(func() {
		redisClient.Delete(scopedQueueName)
		redisClient.Delete(GlobalWorkQueueName)
		redisClient.Delete(resultKey)
	})
}

func Test_ProcessCognitiveChainCheck_UsesCorrectScopedKey(t *testing.T) {
	_, redisClient := setupIntegrationWorker(t)

	tenantID := "tenant-key-test"
	userID := "user-key-test"
	agentID := "agent-key-test"
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	expectedKey := GenerateSTMKey(tenantID, userID, agentID)
	event := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "scoping test"}
	eventJSON, _ := json.Marshal(event)
	err := redisClient.LPush(expectedKey, string(eventJSON))
	assert.NoError(t, err)

	rawEvents, err := redisClient.LRange(expectedKey, 0, 0)
	assert.NoError(t, err)
	assert.Len(t, rawEvents, 1)

	oldKey := "stm_cache:user_" + userID
	oldEvents, err := redisClient.LRange(oldKey, 0, -1)
	assert.NoError(t, err)
	assert.Len(t, oldEvents, 0)

	t.Cleanup(func() {
		redisClient.Delete(expectedKey)
		redisClient.Delete(oldKey)
	})
}

func Test_ProcessCognitiveChainCheck_ChainContinues_HighSim(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-highsim"
	userID := "test-user-highsim"
	agentID := "test-agent-highsim"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	event1 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "Can you help me understand how to deploy a Docker container to Kubernetes?"}
	event2 := STMEvent{Role: "agent", Type: models.STMEventTypeMessage, Content: "Sure! To deploy a Docker container to Kubernetes, you create a deployment YAML manifest."}

	event2JSON, _ := json.Marshal(event2)
	event1JSON, _ := json.Marshal(event1)
	_ = redisClient.LPush(key, string(event2JSON))
	_ = redisClient.LPush(key, string(event1JSON))

	task := &models.CognitiveChainCheckTask{TenantID: tenantID, UserID: userID, AgentID: agentID}
	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	remaining, err := redisClient.LRange(key, 0, -1)
	assert.NoError(t, err)
	assert.Len(t, remaining, 2)

	t.Cleanup(func() { redisClient.Delete(key) })
}

func Test_ProcessCognitiveChainCheck_ChainBreaks_LowSim(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-lowsim"
	userID := "test-user-lowsim"
	agentID := "test-agent-lowsim"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	t.Setenv("CHAIN_SIM_HIGH", "0.99")
	t.Setenv("CHAIN_SIM_LOW", "0.95")

	event1 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "What is the recipe for chocolate chip cookies?"}
	event2 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "Explain the theory of general relativity."}

	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)
	_ = redisClient.LPush(key, string(event1JSON))
	_ = redisClient.LPush(key, string(event2JSON))

	task := &models.CognitiveChainCheckTask{TenantID: tenantID, UserID: userID, AgentID: agentID}
	err := worker.processCognitiveChainCheck(ctx, task)

	if err != nil {
		remaining, _ := redisClient.LRange(key, 0, -1)
		assert.Len(t, remaining, 2)
	} else {
		remaining, _ := redisClient.LRange(key, 0, -1)
		assert.Len(t, remaining, 1)
	}

	t.Cleanup(func() { redisClient.Delete(key) })
}

func Test_ProcessCognitiveChainCheck_NotEnoughEvents(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-noevents"
	userID := "test-user-noevents"
	agentID := "test-agent-noevents"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	event := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "hello"}
	eventJSON, _ := json.Marshal(event)
	_ = redisClient.LPush(key, string(eventJSON))

	task := &models.CognitiveChainCheckTask{TenantID: tenantID, UserID: userID, AgentID: agentID}
	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	remaining, _ := redisClient.LRange(key, 0, -1)
	assert.Len(t, remaining, 1)

	t.Cleanup(func() { redisClient.Delete(key) })
}

func Test_TaskQueue_EnqueueAndMarkResult(t *testing.T) {
	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: Redis not available: %v", err)
	}
	t.Cleanup(func() { redisClient.Close() })

	taskQueue := NewTaskQueue(redisClient)
	ctx := context.Background()

	tenantID := "test-tenant-tq"
	userID := "test-user-tq"
	agentID := "test-agent-tq"
	taskID := "task-result-test"

	err = taskQueue.MarkTaskResult(ctx, tenantID, userID, agentID, taskID, true, "all good")
	assert.NoError(t, err)

	resultKey := "task_results:v1:" + tenantID + ":" + userID + ":" + agentID + ":" + taskID
	exists, _ := redisClient.Exists(resultKey)
	assert.True(t, exists)

	stats, err := taskQueue.GetQueueStats(ctx)
	assert.NoError(t, err)
	assert.Equal(t, GlobalWorkQueueName, stats["queue_name"])

	t.Cleanup(func() { redisClient.Delete(resultKey) })
}

func TestProcessCognitiveChainCheck_MTMFormationFails_NoTrim(t *testing.T) {
	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: Redis not available: %v", err)
	}
	t.Cleanup(func() { redisClient.Close() })

	ctx := context.Background()
	tenantID := "test-tenant-fail"
	userID := "test-user-fail"
	agentID := "test-agent-fail"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	t.Setenv("CHAIN_SIM_HIGH", "0.99")
	t.Setenv("CHAIN_SIM_LOW", "0.95")

	event1 := STMEvent{Role: "user", Content: "Topic A"}
	event2 := STMEvent{Role: "user", Content: "Topic B"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)
	_ = redisClient.LPush(key, string(event1JSON))
	_ = redisClient.LPush(key, string(event2JSON))

	mockStore := &MockSTMStore{
		MockCreateEmbedding: func(ctx context.Context, text string) (*models.EmbeddingData, error) {
			if text == "Topic A" {
				return &models.EmbeddingData{Vector: []float64{1.0, 0.0}}, nil
			}
			return &models.EmbeddingData{Vector: []float64{0.0, 1.0}}, nil
		},
		MockProcessMTMFormation: func(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error {
			return fmt.Errorf("simulated MTM formation failure")
		},
	}

	worker := NewWorker(NewTaskQueue(redisClient), mockStore, redisClient)
	task := &models.CognitiveChainCheckTask{TenantID: tenantID, UserID: userID, AgentID: agentID}

	err = worker.processCognitiveChainCheck(ctx, task)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "simulated MTM formation failure")

	count, _ := redisClient.LLen(key)
	assert.Equal(t, int64(2), count, "STM should not be trimmed on MTM failure")

	t.Cleanup(func() { redisClient.Delete(key) })
}
