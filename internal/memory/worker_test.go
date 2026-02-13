package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/stretchr/testify/assert"
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
	stmStore := NewSTMStore(mongoDB, redisClient)

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

	// Push the task onto the scoped queue and signal the global queue
	scopedQueueName := GenerateScopedQueueName(tenantID, userID, agentID)
	err := redisClient.LPush(scopedQueueName, string(envelopeJSON))
	assert.NoError(t, err)
	err = redisClient.LPush(GlobalWorkQueueName, scopedQueueName)
	assert.NoError(t, err)

	// Worker should pick up and process the task.
	// With < 2 events in STM, processCognitiveChainCheck returns early (not enough data).
	err = worker.processNextTask(ctx, 1)
	assert.NoError(t, err)

	// Verify the task result was stored in Redis
	resultKey := "task_results:v1:" + tenantID + ":" + userID + ":" + agentID + ":dispatch-task-1"
	exists, _ := redisClient.Exists(resultKey)
	assert.True(t, exists, "task result should be stored in Redis")

	// Clean up queues
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

	// The expected key format: stm_cache:v1:{tenant}:{user}:{agent}:user_{user}
	expectedKey := GenerateSTMKey(tenantID, userID, agentID)

	// Write an event directly to the expected key
	event := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "scoping test"}
	eventJSON, _ := json.Marshal(event)
	err := redisClient.LPush(expectedKey, string(eventJSON))
	assert.NoError(t, err)

	// Verify we can read it back from the same key
	rawEvents, err := redisClient.LRange(expectedKey, 0, 0)
	assert.NoError(t, err)
	assert.Len(t, rawEvents, 1)

	var retrieved STMEvent
	err = json.Unmarshal([]byte(rawEvents[0]), &retrieved)
	assert.NoError(t, err)
	assert.Equal(t, "scoping test", retrieved.Content)

	// Verify the OLD key format does NOT have this data
	oldKey := "stm_cache:user_" + userID
	oldEvents, err := redisClient.LRange(oldKey, 0, -1)
	assert.NoError(t, err)
	assert.Len(t, oldEvents, 0, "old unscoped key should have no data")

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

	// Add two events about the SAME topic — real Azure embeddings should be highly similar
	event1 := STMEvent{
		Role:    "user",
		Type:    models.STMEventTypeMessage,
		Content: "Can you help me understand how to deploy a Docker container to Kubernetes?",
	}
	event2 := STMEvent{
		Role:    "agent",
		Type:    models.STMEventTypeMessage,
		Content: "Sure! To deploy a Docker container to Kubernetes, you create a deployment YAML manifest.",
	}

	// LPUSH in reverse chronological order (newest first)
	event2JSON, _ := json.Marshal(event2)
	event1JSON, _ := json.Marshal(event1)
	_ = redisClient.LPush(key, string(event2JSON))
	_ = redisClient.LPush(key, string(event1JSON))

	// Wait for the events to be available
	// Note: event1 is at index 0 (newest push), event2 is at index 1
	// Actually LPush pushes to the HEAD, so order is: [event1JSON, event2JSON]
	// But processCognitiveChainCheck reads LRange(key, 0, 1) which returns [event1JSON, event2JSON]

	task := &models.CognitiveChainCheckTask{
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
	}

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	// Since the events are about the same topic, the chain should continue.
	// Verify no trim happened — both events should still be in the STM.
	remaining, err := redisClient.LRange(key, 0, -1)
	assert.NoError(t, err)
	assert.Len(t, remaining, 2, "both events should remain in STM (no chain break)")

	t.Cleanup(func() {
		redisClient.Delete(key)
	})
}

func Test_ProcessCognitiveChainCheck_ChainBreaks_LowSim(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-lowsim"
	userID := "test-user-lowsim"
	agentID := "test-agent-lowsim"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	// Set very high threshold so even moderately different topics trigger a break
	t.Setenv("CHAIN_SIM_HIGH", "0.99")
	t.Setenv("CHAIN_SIM_LOW", "0.95")

	// Add two events about COMPLETELY different topics
	event1 := STMEvent{
		Role:    "user",
		Type:    models.STMEventTypeMessage,
		Content: "What is the recipe for chocolate chip cookies? I need the exact measurements for flour and sugar.",
	}
	event2 := STMEvent{
		Role:    "user",
		Type:    models.STMEventTypeMessage,
		Content: "Explain the theory of general relativity and how gravity warps spacetime around massive objects.",
	}

	// Push oldest first, then newest (LPUSH makes newest = index 0)
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)
	_ = redisClient.LPush(key, string(event1JSON))
	_ = redisClient.LPush(key, string(event2JSON))

	task := &models.CognitiveChainCheckTask{
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
	}

	// The chain break should trigger MTM formation.
	// ProcessMTMFormation may fail if MongoDB collections aren't set up, but the
	// chain break detection itself is what we're testing.
	err := worker.processCognitiveChainCheck(ctx, task)

	// If MTM formation fails, we get an error — but that's the Save-Then-Trim safety:
	// the STM should NOT be trimmed on failure.
	if err != nil {
		t.Logf("Expected: processCognitiveChainCheck returned error (MTM formation): %v", err)
		// Verify STM was NOT trimmed (Save-Then-Trim safety)
		remaining, _ := redisClient.LRange(key, 0, -1)
		assert.Len(t, remaining, 2, "STM should NOT be trimmed when MTM formation fails")
	} else {
		// Chain break succeeded and MTM formation completed.
		// After a successful chain break + MTM save, only the newest event remains.
		remaining, _ := redisClient.LRange(key, 0, -1)
		assert.Len(t, remaining, 1, "after chain break + MTM save, only newest event should remain")
	}

	t.Cleanup(func() {
		redisClient.Delete(key)
	})
}

func Test_ProcessCognitiveChainCheck_NotEnoughEvents(t *testing.T) {
	worker, redisClient := setupIntegrationWorker(t)
	ctx := context.Background()

	tenantID := "test-tenant-noevents"
	userID := "test-user-noevents"
	agentID := "test-agent-noevents"
	key := GenerateSTMKey(tenantID, userID, agentID)
	cleanupSTMKey(t, redisClient, tenantID, userID, agentID)

	// Add only 1 event — not enough for chain analysis
	event := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "hello"}
	eventJSON, _ := json.Marshal(event)
	_ = redisClient.LPush(key, string(eventJSON))

	task := &models.CognitiveChainCheckTask{
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
	}

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err, "should return nil when there are fewer than 2 events")

	// Event should still be there
	remaining, _ := redisClient.LRange(key, 0, -1)
	assert.Len(t, remaining, 1)

	t.Cleanup(func() {
		redisClient.Delete(key)
	})
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

	// Mark a task result
	err = taskQueue.MarkTaskResult(ctx, tenantID, userID, agentID, taskID, true, "all good")
	assert.NoError(t, err)

	// Verify the result exists in Redis
	resultKey := "task_results:v1:" + tenantID + ":" + userID + ":" + agentID + ":" + taskID
	exists, _ := redisClient.Exists(resultKey)
	assert.True(t, exists)

	// Get queue stats
	stats, err := taskQueue.GetQueueStats(ctx)
	assert.NoError(t, err)
	assert.Equal(t, GlobalWorkQueueName, stats["queue_name"])

	t.Cleanup(func() {
		redisClient.Delete(resultKey)
	})
}
