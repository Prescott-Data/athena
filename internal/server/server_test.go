package server

import (
	"context"
	"testing"

	gen "bitbucket.org/dromos/memory-os/api/grpc/gen"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

// MockSTMCache is a mock implementation of the stmCache interface.
type MockSTMCache struct {
	mock.Mock
}

func (m *MockSTMCache) AddSTMEvent(ctx context.Context, userID string, event memory.STMEvent) error {
	args := m.Called(ctx, userID, event)
	return args.Error(0)
}

// MockTaskQueue is a mock implementation of the taskQueue interface.
type MockTaskQueue struct {
	mock.Mock
}

func (m *MockTaskQueue) EnqueueCognitiveCheckTask(ctx context.Context, tenantID, userID, agentID string) error {
	args := m.Called(ctx, tenantID, userID, agentID)
	return args.Error(0)
}

// --- Tests ---

func TestStoreInteraction_SmartTrigger(t *testing.T) {
	ctx := context.Background()

	t.Run("Should trigger worker for user message and save both events", func(t *testing.T) {
		// Arrange
		mockCache := new(MockSTMCache)
		mockQueue := new(MockTaskQueue)

		server := &MemoryServer{
			stmCache:  mockCache,
			taskQueue: mockQueue,
		}
		// Mock the database call
		server.getIDsFromSessionFunc = func(ctx context.Context, s *MemoryServer, sessionID string) (string, string, string, error) {
			return "test-tenant", "test-user", "test-agent", nil
		}

		req := &gen.StoreInteractionRequest{
			SessionId:     "test-session",
			UserMessage:   "Hello, world!",
			AgentResponse: "Hello to you too!",
		}

		// Expect the user event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-user", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()
		// Expect the agent event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-user", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()
		// Expect the task queue to be triggered exactly once
		mockQueue.On("EnqueueCognitiveCheckTask", ctx, "test-tenant", "test-user", "test-agent").Return(nil).Once()

		// Act
		_, err := server.StoreInteraction(ctx, req)

		// Assert
		assert.NoError(t, err)
		mockCache.AssertExpectations(t)
		mockQueue.AssertExpectations(t)
	})

	t.Run("Should only save user event and trigger worker when no agent response", func(t *testing.T) {
		// Arrange
		mockCache := new(MockSTMCache)
		mockQueue := new(MockTaskQueue)

		server := &MemoryServer{
			stmCache:  mockCache,
			taskQueue: mockQueue,
		}
		server.getIDsFromSessionFunc = func(ctx context.Context, s *MemoryServer, sessionID string) (string, string, string, error) {
			return "test-tenant", "test-user", "test-agent", nil
		}

		// This request only has a user message
		req := &gen.StoreInteractionRequest{
			SessionId:     "test-session",
			UserMessage:   "User message that triggers the call",
			AgentResponse: "", // No agent response in this turn
		}

		// Expect the user event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-user", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()
		// Expect the task queue to be triggered for the user message
		mockQueue.On("EnqueueCognitiveCheckTask", ctx, "test-tenant", "test-user", "test-agent").Return(nil).Once()

		// Act
		_, err := server.StoreInteraction(ctx, req)

		// Assert
		assert.NoError(t, err)
		mockCache.AssertExpectations(t)
		mockQueue.AssertExpectations(t)
	})
}
