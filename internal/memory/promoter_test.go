package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"bitbucket.org/dromos/athena-memos/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// --- Test Cases ---

func TestPromoter_RunOnce_PromotesHotChain(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	// Note: mt.Close() not available in mongo-driver v1.13.1

	mt.Run("Test promotion of a hot chain", func(mt *mtest.T) {
		// 1. Setup
		// We need to initialize the HeatScorer, which will use default config
		heatScorer := NewHeatScorer(mt.DB)
		promoter := &Promoter{
			db:         mt.DB,
			heatScorer: heatScorer,
		}

		// Mock data for a "hot" chain
		now := time.Now()
		hotChainID := primitive.NewObjectID()
		hotChain := models.CognitiveChain{
			ChainID:             "hot-chain-1",
			TenantID:            "test-tenant",
			UserID:              "test-user",
			Summary:             "This is an important topic.",
			IntrinsicImportance: 1.0,  // High importance
			RecallStrength:      10.0, // High recall strength to minimize decay
			LastAccessedAt:      &now, // Just accessed
			StartedAt:           now.Add(-1 * time.Hour),
			LastEventAt:         now,
			Status:              "in_mtm",
		}
		// The heat score for this will be high (~0.8 or more depending on weights)

		// Configure mtest to return this chain (use camelCase for BSON tags)
		findResponse := mtest.CreateCursorResponse(1, "memory_os_e2e.cognitive_chains", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: hotChainID},
			{Key: "chainId", Value: hotChain.ChainID},
			{Key: "tenantId", Value: hotChain.TenantID},
			{Key: "userId", Value: hotChain.UserID},
			{Key: "summary", Value: hotChain.Summary},
			{Key: "intrinsicImportance", Value: hotChain.IntrinsicImportance},
			{Key: "recallStrength", Value: hotChain.RecallStrength},
			{Key: "lastAccessedAt", Value: hotChain.LastAccessedAt},
			{Key: "startedAt", Value: hotChain.StartedAt},
			{Key: "lastEventAt", Value: hotChain.LastEventAt},
			{Key: "status", Value: hotChain.Status},
			{Key: "eventCount", Value: 5},
		})
		killCursorsResponse := mtest.CreateCursorResponse(0, "memory_os_e2e.cognitive_chains", mtest.NextBatch)
		// Mock for the update call after heat calculation
		updateResponse := mtest.CreateSuccessResponse()
		mt.AddMockResponses(findResponse, killCursorsResponse, updateResponse)

		// 2. Action
		err := promoter.RunOnce(context.Background(), 0.5) // Heat threshold of 0.5

		// 3. Assert
		if err != nil {
			t.Fatalf("Promoter.RunOnce failed: %v", err)
		}
	})
}

func TestPromoter_RunOnce_IgnoresColdChain(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	// Note: mt.Close() not available in mongo-driver v1.13.1

	mt.Run("Test ignoring a cold chain", func(mt *mtest.T) {
		// 1. Setup
		heatScorer := NewHeatScorer(mt.DB)
		promoter := &Promoter{
			db:         mt.DB,
			heatScorer: heatScorer,
		}

		// Mock data for a "cold" chain
		coldChainID := primitive.NewObjectID()
		oldTime := time.Now().Add(-300 * time.Hour)
		coldChain := models.CognitiveChain{
			ChainID:             "cold-chain-1",
			TenantID:            "test-tenant",
			UserID:              "test-user",
			Summary:             "A trivial topic.",
			IntrinsicImportance: 0.1,      // Low importance
			RecallStrength:      1.0,      // Baseline recall
			LastAccessedAt:      &oldTime, // Very old access
			StartedAt:           oldTime,
			LastEventAt:         oldTime,
			Status:              "in_mtm",
		}
		// The heat score for this will be very low

		// Configure mtest to return this chain (use camelCase for BSON tags)
		findResponse := mtest.CreateCursorResponse(1, "memory_os_e2e.cognitive_chains", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: coldChainID},
			{Key: "chainId", Value: coldChain.ChainID},
			{Key: "tenantId", Value: coldChain.TenantID},
			{Key: "userId", Value: coldChain.UserID},
			{Key: "summary", Value: coldChain.Summary},
			{Key: "intrinsicImportance", Value: coldChain.IntrinsicImportance},
			{Key: "recallStrength", Value: coldChain.RecallStrength},
			{Key: "lastAccessedAt", Value: coldChain.LastAccessedAt},
			{Key: "startedAt", Value: coldChain.StartedAt},
			{Key: "lastEventAt", Value: coldChain.LastEventAt},
			{Key: "status", Value: coldChain.Status},
			{Key: "eventCount", Value: 1},
		})
		killCursorsResponse := mtest.CreateCursorResponse(0, "memory_os_e2e.cognitive_chains", mtest.NextBatch)
		updateResponse := mtest.CreateSuccessResponse()
		mt.AddMockResponses(findResponse, killCursorsResponse, updateResponse)

		// 2. Action
		err := promoter.RunOnce(context.Background(), 0.9) // High heat threshold of 0.9

		// 3. Assert
		if err != nil {
			t.Fatalf("Promoter.RunOnce failed: %v", err)
		}
	})
}

// --- Integration Mock Services ---

// MockGraphExtractor is a mock implementation of GraphExtractor
type MockGraphExtractor struct {
	mock.Mock
}

func (m *MockGraphExtractor) ExtractGraphFromSummary(ctx context.Context, summary string) (*GraphExtraction, error) {
	args := m.Called(ctx, summary)
	if args.Get(0) != nil {
		return args.Get(0).(*GraphExtraction), args.Error(1)
	}
	return nil, args.Error(1)
}

// MockGraphWriter is a mock implementation of GraphWriter
type MockGraphWriter struct {
	mock.Mock
}

func (m *MockGraphWriter) WriteExtractionToGraph(ctx context.Context, extraction *GraphExtraction, heatScore float64) error {
	args := m.Called(ctx, extraction, heatScore)
	return args.Error(0)
}

func TestPromoteChainToLPM_Success(t *testing.T) {
	mockExtractor := new(MockGraphExtractor)
	mockWriter := new(MockGraphWriter)

	promoter := &Promoter{
		extractor: mockExtractor,
		writer:    mockWriter,
	}

	chain := &models.CognitiveChain{
		ChainID: "chain-123",
		Summary: "Test summary for success path",
	}
	heatScore := 0.9

	expectedExtraction := &GraphExtraction{
		Nodes: []GraphNode{{ID: "1", Label: "Identities", Name: "Test Node"}},
	}

	mockExtractor.On("ExtractGraphFromSummary", mock.Anything, "Test summary for success path").Return(expectedExtraction, nil)
	mockWriter.On("WriteExtractionToGraph", mock.Anything, expectedExtraction, 0.9).Return(nil)

	err := promoter.promoteChainToLPM(context.Background(), chain, heatScore)
	assert.NoError(t, err)

	mockExtractor.AssertExpectations(t)
	mockExtractor.AssertNumberOfCalls(t, "ExtractGraphFromSummary", 1)

	mockWriter.AssertExpectations(t)
	mockWriter.AssertNumberOfCalls(t, "WriteExtractionToGraph", 1)
}

func TestPromoteChainToLPM_ExtractorFails(t *testing.T) {
	mockExtractor := new(MockGraphExtractor)
	mockWriter := new(MockGraphWriter)

	promoter := &Promoter{
		extractor: mockExtractor,
		writer:    mockWriter,
	}

	chain := &models.CognitiveChain{
		ChainID: "chain-123",
		Summary: "Test summary for failure path",
	}
	heatScore := 0.9

	expectedErr := errors.New("llm extraction failed test error")
	mockExtractor.On("ExtractGraphFromSummary", mock.Anything, "Test summary for failure path").Return(nil, expectedErr)

	err := promoter.promoteChainToLPM(context.Background(), chain, heatScore)

	// Ensure the error is returned back correctly
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to extract graph")

	mockExtractor.AssertExpectations(t)
	mockExtractor.AssertNumberOfCalls(t, "ExtractGraphFromSummary", 1)

	// Crucial: Writer should NEVER be called if extraction fails
	mockWriter.AssertNotCalled(t, "WriteExtractionToGraph")
}
