package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MockTaskQueuer is a mock implementation of the TaskQueuer for testing
type MockTaskQueuer struct {
	mock.Mock
}

func (m *MockTaskQueuer) GetQueueStats(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockTaskQueuer) MarkTaskResult(ctx context.Context, tenantID, userID, agentID, taskID string, success bool, result string) error {
	args := m.Called(ctx, tenantID, userID, agentID, taskID, success, result)
	return args.Error(0)
}

// MockSTMStore is a mock implementation of the stmStore interface for testing
type MockSTMStore struct {
	mock.Mock
}

func (m *MockSTMStore) CreateEmbedding(ctx context.Context, textToEmbed string) (*models.EmbeddingData, error) {
	args := m.Called(ctx, textToEmbed)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.EmbeddingData), args.Error(1)
}

func (m *MockSTMStore) analyzeTopicContinuity(ctx context.Context, userID, previousContent, newContent string) (bool, error) {
	args := m.Called(ctx, userID, previousContent, newContent)
	return args.Bool(0), args.Error(1)
}

func (m *MockSTMStore) ProcessMTMFormation(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error {
	args := m.Called(ctx, tenantID, userID, agentID, events)
	return args.Error(0)
}

func (m *MockSTMStore) StoreCognitiveEvent(ctx context.Context, event *models.CognitiveEvent) (primitive.ObjectID, error) {
	args := m.Called(ctx, event)
	return args.Get(0).(primitive.ObjectID), args.Error(1)
}

func (m *MockSTMStore) SearchSimilarChains(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]*models.CognitiveChain, error) {
	args := m.Called(ctx, tenantID, userID, agentID, queryVector, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.CognitiveChain), args.Error(1)
}

// Test Helpers
func setupWorkerTest() (*Worker, *MockTaskQueuer, *MockSTMStore, *MockRedis) {
	mockTaskQueue := new(MockTaskQueuer)
	mockSTMStore := new(MockSTMStore)
	mockRedis := new(MockRedis)

	worker := NewWorker(mockTaskQueue, mockSTMStore, mockRedis)

	return worker, mockTaskQueue, mockSTMStore, mockRedis
}

func Test_ProcessNextTask_DispatchesCorrectly(t *testing.T) {
	worker, mockTaskQueue, _, mockRedis := setupWorkerTest()
	ctx := context.Background()

	taskPayload := models.CognitiveChainCheckTask{ID: "test-task", TenantID: "test-tenant", UserID: "test-user", AgentID: "test-agent"}
	payloadJSON, _ := json.Marshal(taskPayload)
	envelope := &TaskEnvelope{
		ID:      "test-task",
		Type:    TaskTypeCognitiveChainCheck,
		Payload: payloadJSON,
	}
	envelopeJSON, _ := json.Marshal(envelope)

	scopedQueueName := GenerateScopedQueueName(taskPayload.TenantID, taskPayload.UserID, taskPayload.AgentID)

	mockRedis.On("BRPop", mock.Anything, []string{GlobalWorkQueueName}).Return([]string{GlobalWorkQueueName, scopedQueueName}, nil).Once()
	mockRedis.On("RPop", scopedQueueName).Return(string(envelopeJSON), nil).Once()
	mockTaskQueue.On("MarkTaskResult", mock.Anything, taskPayload.TenantID, taskPayload.UserID, taskPayload.AgentID, taskPayload.ID, true, "success").Return(nil)
	mockRedis.On("LRange", mock.Anything, mock.Anything, mock.Anything).Return([]string{}, nil) // Prevent panic

	err := worker.processNextTask(ctx, 1)
	assert.NoError(t, err)

	mockTaskQueue.AssertExpectations(t)
	mockRedis.AssertExpectations(t)
}

func Test_ProcessCognitiveChainCheck_ChainContinues_HighSim(t *testing.T) {
	worker, _, mockSTMStore, mockRedis := setupWorkerTest()
	ctx := context.Background()
	task := &models.CognitiveChainCheckTask{UserID: "test-user"}

	// Mock events
	event1 := STMEvent{Content: "similar content 1"}
	event2 := STMEvent{Content: "similar content 2"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)

	mockRedis.On("LRange", mock.Anything, int64(0), int64(1)).Return([]string{string(event2JSON), string(event1JSON)}, nil).Once()

	// Mock embeddings to produce high similarity
	mockSTMStore.On("CreateEmbedding", ctx, event2.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()
	mockSTMStore.On("CreateEmbedding", ctx, event1.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	// Assertions
	mockSTMStore.AssertNotCalled(t, "analyzeTopicContinuity", mock.Anything, mock.Anything, mock.Anything)
	mockRedis.AssertNotCalled(t, "LTrim", mock.Anything, mock.Anything, mock.Anything)
	mockSTMStore.AssertNotCalled(t, "ProcessMTMFormation", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func Test_ProcessCognitiveChainCheck_ChainBreaks_LowSim(t *testing.T) {
	worker, _, mockSTMStore, mockRedis := setupWorkerTest()
	ctx := context.Background()
	task := &models.CognitiveChainCheckTask{UserID: "test-user"}

	// Mock events
	event1 := STMEvent{Content: "different content 1"}
	event2 := STMEvent{Content: "different content 2"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)

	mockRedis.On("LRange", mock.Anything, int64(0), int64(1)).Return([]string{string(event2JSON), string(event1JSON)}, nil).Once()

	// Mock embeddings to produce low similarity
	mockSTMStore.On("CreateEmbedding", ctx, event2.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()
	mockSTMStore.On("CreateEmbedding", ctx, event1.Content).Return(&models.EmbeddingData{Vector: []float64{0, 1}}, nil).Once()

	mockRedis.On("LRange", mock.Anything, int64(1), int64(-1)).Return([]string{string(event1JSON)}, nil).Once()
	mockSTMStore.On("ProcessMTMFormation", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockRedis.On("LTrim", mock.Anything, int64(0), int64(0)).Return(nil).Once()

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	// Assertions
	mockSTMStore.AssertExpectations(t)
	mockRedis.AssertExpectations(t)
}

func Test_ProcessCognitiveChainCheck_GrayArea_LLMFallback_Continues(t *testing.T) {
	t.Setenv("CHAIN_SIM_HIGH", "0.9")
	t.Setenv("CHAIN_SIM_LOW", "0.7")

	worker, _, mockSTMStore, mockRedis := setupWorkerTest()
	ctx := context.Background()
	task := &models.CognitiveChainCheckTask{UserID: "test-user"}

	// Mock events
	event1 := STMEvent{Content: "gray area content 1"}
	event2 := STMEvent{Content: "gray area content 2"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)

	mockRedis.On("LRange", mock.Anything, int64(0), int64(1)).Return([]string{string(event2JSON), string(event1JSON)}, nil).Once()

	// Mock embeddings to produce gray area similarity
	mockSTMStore.On("CreateEmbedding", ctx, event2.Content).Return(&models.EmbeddingData{Vector: []float64{0.8, 0.6}}, nil).Once()
	mockSTMStore.On("CreateEmbedding", ctx, event1.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()

	mockSTMStore.On("analyzeTopicContinuity", ctx, "test-user", event2.Content, event1.Content).Return(true, nil).Once()

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	// Assertions
	mockRedis.AssertNotCalled(t, "LTrim", mock.Anything, mock.Anything, mock.Anything)
	mockSTMStore.AssertNotCalled(t, "ProcessMTMFormation", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func Test_ProcessCognitiveChainCheck_GrayArea_LLMFallback_Breaks(t *testing.T) {
	t.Setenv("CHAIN_SIM_HIGH", "0.9")
	t.Setenv("CHAIN_SIM_LOW", "0.7")

	worker, _, mockSTMStore, mockRedis := setupWorkerTest()
	ctx := context.Background()
	task := &models.CognitiveChainCheckTask{UserID: "test-user"}

	// Mock events
	event1 := STMEvent{Content: "gray area content 1"}
	event2 := STMEvent{Content: "gray area content 2"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)

	mockRedis.On("LRange", mock.Anything, int64(0), int64(1)).Return([]string{string(event2JSON), string(event1JSON)}, nil).Once()

	// Mock embeddings to produce gray area similarity
	mockSTMStore.On("CreateEmbedding", ctx, event2.Content).Return(&models.EmbeddingData{Vector: []float64{0.8, 0.6}}, nil).Once()
	mockSTMStore.On("CreateEmbedding", ctx, event1.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()

	mockSTMStore.On("analyzeTopicContinuity", ctx, "test-user", event2.Content, event1.Content).Return(false, nil).Once()

	mockRedis.On("LRange", mock.Anything, int64(1), int64(-1)).Return([]string{string(event1JSON)}, nil).Once()
	mockSTMStore.On("ProcessMTMFormation", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockRedis.On("LTrim", mock.Anything, int64(0), int64(0)).Return(nil).Once()

	err := worker.processCognitiveChainCheck(ctx, task)
	assert.NoError(t, err)

	// Assertions
	mockSTMStore.AssertExpectations(t)
	mockRedis.AssertExpectations(t)
}

func Test_ProcessCognitiveChainCheck_ChainBreaks_MTMFailure(t *testing.T) {
	worker, _, mockSTMStore, mockRedis := setupWorkerTest()
	ctx := context.Background()
	task := &models.CognitiveChainCheckTask{UserID: "test-user"}

	// Mock events for low similarity
	event1 := STMEvent{Content: "different content 1"}
	event2 := STMEvent{Content: "different content 2"}
	event1JSON, _ := json.Marshal(event1)
	event2JSON, _ := json.Marshal(event2)

	mockRedis.On("LRange", mock.Anything, int64(0), int64(1)).Return([]string{string(event2JSON), string(event1JSON)}, nil).Once()

	// Mock embeddings to produce low similarity
	mockSTMStore.On("CreateEmbedding", ctx, event2.Content).Return(&models.EmbeddingData{Vector: []float64{1, 0}}, nil).Once()
	mockSTMStore.On("CreateEmbedding", ctx, event1.Content).Return(&models.EmbeddingData{Vector: []float64{0, 1}}, nil).Once()

	// Mock the retrieval of the old chain
	mockRedis.On("LRange", mock.Anything, int64(1), int64(-1)).Return([]string{string(event1JSON)}, nil).Once()

	// CRITICAL: Mock ProcessMTMFormation to return an error
	simulatedErr := errors.New("simulated database failure")
	mockSTMStore.On("ProcessMTMFormation", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(simulatedErr).Once()

	// Act
	err := worker.processCognitiveChainCheck(ctx, task)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, "MTM formation failed: simulated database failure", err.Error())

	// Most importantly, assert that LTrim was NEVER called
	mockRedis.AssertNotCalled(t, "LTrim", mock.Anything, mock.Anything, mock.Anything)

	// Verify other mocks were called as expected
	mockSTMStore.AssertExpectations(t)
	mockRedis.AssertExpectations(t)
}
