package server

import (
	"context"
	"errors"
	"testing"

	gen "github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Mocks ---

// MockSTMCache is a mock implementation of the stmCache interface.
type MockSTMCache struct {
	mock.Mock
}

func (m *MockSTMCache) AddSTMEvent(ctx context.Context, tenantID, userID, agentID string, event memory.STMEvent) error {
	args := m.Called(ctx, tenantID, userID, agentID, event)
	return args.Error(0)
}

// MockRedis is a mock implementation of the cache.Interface for testing
type MockRedis struct {
	cache.Interface
	mock.Mock
}

func (m *MockRedis) LPush(key string, values ...interface{}) error {
	args := m.Called(key, values)
	return args.Error(0)
}

// --- Tests ---

func TestStoreInteraction_SmartTrigger(t *testing.T) {
	ctx := context.Background()

	t.Run("Should trigger worker for user message and save both events", func(t *testing.T) {
		// Arrange
		mockCache := new(MockSTMCache)
		mockRedis := new(MockRedis)

		server := &MemoryServer{
			stmCache:    mockCache,
			redisClient: mockRedis,
		}
		// Mock the database call
		server.getIDsFromSessionFunc = func(s *MemoryServer, ctx context.Context, sessionID string) (string, string, string, error) {
			return "test-tenant", "test-user", "test-agent", nil
		}

		req := &gen.StoreInteractionRequest{
			SessionId:     "test-session",
			UserMessage:   "Hello, world!",
			AgentResponse: "Hello to you too!",
		}

		// Expect the user event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-tenant", "test-user", "test-agent", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()
		// Expect the agent event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-tenant", "test-user", "test-agent", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()

		// Expect the two-step enqueue
		scopedQueueName := memory.GenerateScopedQueueName("test-tenant", "test-user", "test-agent")
		mockRedis.On("LPush", scopedQueueName, mock.Anything).Return(nil).Once()
		mockRedis.On("LPush", memory.GlobalWorkQueueName, []interface{}{scopedQueueName}).Return(nil).Once()

		// Act
		_, err := server.StoreInteraction(ctx, req)

		// Assert
		assert.NoError(t, err)
		mockCache.AssertExpectations(t)
		mockRedis.AssertExpectations(t)
	})

	t.Run("Should only save user event and trigger worker when no agent response", func(t *testing.T) {
		// Arrange
		mockCache := new(MockSTMCache)
		mockRedis := new(MockRedis)

		server := &MemoryServer{
			stmCache:    mockCache,
			redisClient: mockRedis,
		}
		server.getIDsFromSessionFunc = func(s *MemoryServer, ctx context.Context, sessionID string) (string, string, string, error) {
			return "test-tenant", "test-user", "test-agent", nil
		}

		// This request only has a user message
		req := &gen.StoreInteractionRequest{
			SessionId:     "test-session",
			UserMessage:   "User message that triggers the call",
			AgentResponse: "", // No agent response in this turn
		}

		// Expect the user event to be saved
		mockCache.On("AddSTMEvent", ctx, "test-tenant", "test-user", "test-agent", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()

		// Expect the two-step enqueue
		scopedQueueName := memory.GenerateScopedQueueName("test-tenant", "test-user", "test-agent")
		mockRedis.On("LPush", scopedQueueName, mock.Anything).Return(nil).Once()
		mockRedis.On("LPush", memory.GlobalWorkQueueName, []interface{}{scopedQueueName}).Return(nil).Once()

		// Act
		_, err := server.StoreInteraction(ctx, req)

		// Assert
		assert.NoError(t, err)
		mockCache.AssertExpectations(t)
		mockRedis.AssertExpectations(t)
	})
}

func TestStoreEventReturnsDurableEventID(t *testing.T) {
	ctx := context.Background()
	mockCache := new(MockSTMCache)
	mockRedis := new(MockRedis)
	eventID := primitive.NewObjectID()
	server := &MemoryServer{
		stmCache:    mockCache,
		redisClient: mockRedis,
		persistCognitiveEvent: func(context.Context, *models.CognitiveEvent) (primitive.ObjectID, error) {
			return eventID, nil
		},
	}
	server.getIDsFromSessionFunc = func(*MemoryServer, context.Context, string) (string, string, string, error) {
		return "test-tenant", "test-user", "test-agent", nil
	}
	mockCache.On("AddSTMEvent", ctx, "test-tenant", "test-user", "test-agent", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()
	scopedQueueName := memory.GenerateScopedQueueName("test-tenant", "test-user", "test-agent")
	mockRedis.On("LPush", scopedQueueName, mock.Anything).Return(nil).Once()
	mockRedis.On("LPush", memory.GlobalWorkQueueName, []interface{}{scopedQueueName}).Return(nil).Once()

	response, err := server.StoreEvent(ctx, &gen.StoreEventRequest{
		SessionId: "test-session",
		Role:      "agent",
		Type:      "observation",
		Content:   "durable event",
	})

	assert.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, eventID.Hex(), response.EventId)
	mockCache.AssertExpectations(t)
	mockRedis.AssertExpectations(t)
}

func TestStoreEventFailsWhenDurablePersistenceFails(t *testing.T) {
	ctx := context.Background()
	mockCache := new(MockSTMCache)
	server := &MemoryServer{
		stmCache: mockCache,
		persistCognitiveEvent: func(context.Context, *models.CognitiveEvent) (primitive.ObjectID, error) {
			return primitive.NilObjectID, errors.New("mongo unavailable")
		},
	}
	server.getIDsFromSessionFunc = func(*MemoryServer, context.Context, string) (string, string, string, error) {
		return "test-tenant", "test-user", "test-agent", nil
	}
	mockCache.On("AddSTMEvent", ctx, "test-tenant", "test-user", "test-agent", mock.AnythingOfType("memory.STMEvent")).Return(nil).Once()

	response, err := server.StoreEvent(ctx, &gen.StoreEventRequest{
		SessionId: "test-session",
		Role:      "agent",
		Type:      "observation",
		Content:   "not durable",
	})

	assert.Nil(t, response)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "failed to persist event")
	mockCache.AssertExpectations(t)
}
