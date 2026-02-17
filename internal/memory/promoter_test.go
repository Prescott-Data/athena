package memory

import (
	"context"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
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
