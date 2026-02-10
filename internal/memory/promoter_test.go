package memory

import (
	"context"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// MockJanusClient is a mock implementation of the JanusClient interface.
type MockJanusClient struct {
	mock.Mock
}

func (m *MockJanusClient) CreateUserNode(ctx context.Context, tenantID, userID string) error {
	args := m.Called(ctx, tenantID, userID)
	return args.Error(0)
}

func (m *MockJanusClient) AddUserPersonalityTriple(ctx context.Context, tenantID, userID, predicate, object string, confidence float64) error {
	args := m.Called(ctx, tenantID, userID, predicate, object, confidence)
	return args.Error(0)
}

// --- Test Cases ---

func TestPromoter_RunOnce_PromotesHotChain(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	// Note: mt.Close() not available in mongo-driver v1.13.1

	mt.Run("Test promotion of a hot chain", func(mt *mtest.T) {
		// 1. Setup
		mockJanus := new(MockJanusClient)
		promoter := &Promoter{
			db:         mt.DB,
			janus:      mockJanus,
			heatScorer: NewHeatScorer(mt.DB), // Heat scorer also needs the mock DB
		}

		// Mock data for a "hot" chain
		hotChainID := primitive.NewObjectID()
		hotChain := models.CognitiveChain{
			ChainID:     "hot-chain-1",
			TenantID:    "test-tenant",
			UserID:      "test-user",
			Summary:     "This is an important topic.",
			AccessCount: 10, // High access count will lead to a high heat score
			StartedAt:   time.Now().Add(-1 * time.Hour),
			LastEventAt: time.Now(),
			Status:      "in_mtm",
		}
		// The heat score for this will be high, definitely > 0.5

		// Configure mtest to return this chain (use camelCase for BSON tags)
		findResponse := mtest.CreateCursorResponse(1, "memory_os_e2e.cognitive_chains", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: hotChainID},
			{Key: "chainId", Value: hotChain.ChainID},
			{Key: "tenantId", Value: hotChain.TenantID},
			{Key: "userId", Value: hotChain.UserID},
			{Key: "summary", Value: hotChain.Summary},
			{Key: "accessCount", Value: hotChain.AccessCount},
			{Key: "startedAt", Value: hotChain.StartedAt},
			{Key: "lastEventAt", Value: hotChain.LastEventAt},
			{Key: "status", Value: hotChain.Status},
			{Key: "eventCount", Value: 5},
		})
		killCursorsResponse := mtest.CreateCursorResponse(0, "memory_os_e2e.cognitive_chains", mtest.NextBatch)
		// Mock for the update call after heat calculation
		updateResponse := mtest.CreateSuccessResponse()
		mt.AddMockResponses(findResponse, killCursorsResponse, updateResponse)

		// Configure Janus mock expectations
		mockJanus.On("CreateUserNode", mock.Anything, hotChain.TenantID, hotChain.UserID).Return(nil)
		mockJanus.On("AddUserPersonalityTriple", mock.Anything, hotChain.TenantID, hotChain.UserID, "interested_in_topic", hotChain.Summary, mock.AnythingOfType("float64")).Return(nil)

		// 2. Action
		err := promoter.RunOnce(context.Background(), 0.5) // Heat threshold of 0.5

		// 3. Assert
		if err != nil {
			t.Fatalf("Promoter.RunOnce failed: %v", err)
		}
		mockJanus.AssertExpectations(t)
	})
}

func TestPromoter_RunOnce_IgnoresColdChain(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	// Note: mt.Close() not available in mongo-driver v1.13.1

	mt.Run("Test ignoring a cold chain", func(mt *mtest.T) {
		// 1. Setup
		mockJanus := new(MockJanusClient)
		promoter := &Promoter{
			db:         mt.DB,
			janus:      mockJanus,
			heatScorer: NewHeatScorer(mt.DB),
		}

		// Mock data for a "cold" chain
		coldChainID := primitive.NewObjectID()
		coldChain := models.CognitiveChain{
			ChainID:     "cold-chain-1",
			TenantID:    "test-tenant",
			UserID:      "test-user",
			Summary:     "A trivial topic.",
			AccessCount: 0,                                // No access
			StartedAt:   time.Now().Add(-300 * time.Hour), // Very old
			LastEventAt: time.Now().Add(-300 * time.Hour),
			Status:      "in_mtm",
		}
		// The heat score for this will be very low, definitely < 0.9

		// Configure mtest to return this chain (use camelCase for BSON tags)
		findResponse := mtest.CreateCursorResponse(1, "memory_os_e2e.cognitive_chains", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: coldChainID},
			{Key: "chainId", Value: coldChain.ChainID},
			{Key: "tenantId", Value: coldChain.TenantID},
			{Key: "userId", Value: coldChain.UserID},
			{Key: "summary", Value: coldChain.Summary},
			{Key: "accessCount", Value: coldChain.AccessCount},
			{Key: "startedAt", Value: coldChain.StartedAt},
			{Key: "lastEventAt", Value: coldChain.LastEventAt},
			{Key: "status", Value: coldChain.Status},
			{Key: "eventCount", Value: 1},
		})
		killCursorsResponse := mtest.CreateCursorResponse(0, "memory_os_e2e.cognitive_chains", mtest.NextBatch)
		updateResponse := mtest.CreateSuccessResponse()
		mt.AddMockResponses(findResponse, killCursorsResponse, updateResponse)

		// No expectations are set on the Janus mock.

		// 2. Action
		err := promoter.RunOnce(context.Background(), 0.9) // High heat threshold of 0.9

		// 3. Assert
		if err != nil {
			t.Fatalf("Promoter.RunOnce failed: %v", err)
		}
		// Assert that the mock methods were NOT called
		mockJanus.AssertNotCalled(t, "CreateUserNode")
		mockJanus.AssertNotCalled(t, "AddUserPersonalityTriple")
	})
}
