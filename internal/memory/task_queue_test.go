package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)


// Test Helpers
func setupTaskQueueTest() (*TaskQueue, *MockRedis) {
	mockRedis := new(MockRedis)
	taskQueue := NewTaskQueue(mockRedis)
	return taskQueue, mockRedis
}

func Test_EnqueueCognitiveCheckTask(t *testing.T) {
	taskQueue, mockRedis := setupTaskQueueTest()
	ctx := context.Background()
	tenantID := "test-tenant"
	userID := "test-user"
	agentID := "test-agent"

	// Use mock.MatchedBy to capture and inspect the argument
	mockRedis.On("LPush", TaskQueueName, mock.MatchedBy(func(val []interface{}) bool {
		if len(val) != 1 {
			return false
		}
		envelopeJSON, ok := val[0].(string)
		if !ok {
			return false
		}

		var envelope TaskEnvelope
		if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err != nil {
			return false
		}

		assert.Equal(t, TaskTypeCognitiveChainCheck, envelope.Type)

		var payload models.CognitiveChainCheckTask
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return false
		}

		assert.Equal(t, tenantID, payload.TenantID)
		assert.Equal(t, userID, payload.UserID)
		assert.Equal(t, agentID, payload.AgentID)

		return true
	})).Return(nil).Once()

	err := taskQueue.EnqueueCognitiveCheckTask(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)

	mockRedis.AssertExpectations(t)
}

func Test_DequeueTask(t *testing.T) {
	taskQueue, mockRedis := setupTaskQueueTest()
	ctx := context.Background()

	// 1. Create a sample task and envelope
	taskPayload := models.CognitiveChainCheckTask{
		ID:       "test-task-id",
		Type:     TaskTypeCognitiveChainCheck,
		TenantID: "test-tenant",
		UserID:   "test-user",
		AgentID:  "test-agent",
	}
	payloadJSON, _ := json.Marshal(taskPayload)

	envelope := TaskEnvelope{
		ID:         taskPayload.ID,
		Type:       taskPayload.Type,
		Payload:    payloadJSON,
		EnqueuedAt: time.Now(),
	}
	envelopeJSON, _ := json.Marshal(envelope)

	// 2. Mock BRPop to return the sample envelope
	mockRedis.On("BRPop", 1*time.Second, []string{TaskQueueName}).Return([]string{TaskQueueName, string(envelopeJSON)}, nil).Once()

	// 3. Dequeue and assert
	dequeuedEnvelope, err := taskQueue.DequeueTask(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, dequeuedEnvelope)

	assert.Equal(t, envelope.ID, dequeuedEnvelope.ID)
	assert.Equal(t, envelope.Type, dequeuedEnvelope.Type)
	assert.Equal(t, envelope.Payload, dequeuedEnvelope.Payload)

	mockRedis.AssertExpectations(t)
}

func Test_GetQueueStats(t *testing.T) {
	taskQueue, mockRedis := setupTaskQueueTest()
	ctx := context.Background()

	expectedLength := int64(5)

	mockRedis.On("LLen", TaskQueueName).Return(expectedLength, nil).Once()

	stats, err := taskQueue.GetQueueStats(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, stats)

	assert.Equal(t, expectedLength, stats["queue_length"])

	mockRedis.AssertExpectations(t)
}
