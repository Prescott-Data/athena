package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestHeatScorer_Ebbinghaus_Validation(t *testing.T) {
	// Setup Scorer with standard config
	// Tau = 24 hours
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			WeightIntrinsic:    1.0, // Set to 1.0 to isolate decay effect on base importance
			WeightDensity:      0.0,
			DecayTauHours:      24.0,
			RecallGrowthFactor: 1.5,
		},
	}

	ctx := context.Background()
	baseImportance := 0.8

	t.Run("Fresh memory returns I_base", func(t *testing.T) {
		now := time.Now()
		chain := &models.CognitiveChain{
			IntrinsicImportance: baseImportance,
			RecallStrength:      1.0,
			LastAccessedAt:      &now,
		}

		score, _, err := scorer.ComputeSegmentHeat(ctx, chain)
		assert.NoError(t, err)

		// At t=0, decay factor is 1.0, so Score = BaseImportance * 1.0
		// Allow small delta for execution time
		assert.InDelta(t, baseImportance, score, 0.001, "Fresh memory should have score equal to base importance")
	})

	t.Run("Standard memory decays correctly after 24h", func(t *testing.T) {
		twentyFourHoursAgo := time.Now().Add(-24 * time.Hour)
		chain := &models.CognitiveChain{
			IntrinsicImportance: baseImportance,
			RecallStrength:      1.0, // Standard strength
			LastAccessedAt:      &twentyFourHoursAgo,
		}

		score, _, err := scorer.ComputeSegmentHeat(ctx, chain)
		assert.NoError(t, err)

		// Expected Decay: exp(-24 / (24 * 1.0)) = exp(-1) ≈ 0.3679
		expectedScore := baseImportance * 0.36787944117
		assert.InDelta(t, expectedScore, score, 0.001, "Standard memory should decay to ~37% of base after 1 Tau")
	})

	t.Run("High RecallStrength decays slower", func(t *testing.T) {
		twentyFourHoursAgo := time.Now().Add(-24 * time.Hour)

		// Chain A: Standard Strength (S=1.0)
		chainA := &models.CognitiveChain{
			IntrinsicImportance: baseImportance,
			RecallStrength:      1.0,
			LastAccessedAt:      &twentyFourHoursAgo,
		}
		scoreA, _, _ := scorer.ComputeSegmentHeat(ctx, chainA)

		// Chain B: High Strength (S=3.0) - Simulated reinforcement
		chainB := &models.CognitiveChain{
			IntrinsicImportance: baseImportance,
			RecallStrength:      3.0,
			LastAccessedAt:      &twentyFourHoursAgo,
		}
		scoreB, _, _ := scorer.ComputeSegmentHeat(ctx, chainB)

		// Log scores for visibility
		t.Logf("Score A (S=1.0): %.4f", scoreA)
		t.Logf("Score B (S=3.0): %.4f", scoreB)

		// Assertions
		assert.True(t, scoreB > scoreA, "Higher recall strength should result in higher retained score")

		// Calculate expected for B: exp(-24 / (24 * 3.0)) = exp(-1/3) ≈ 0.7165
		expectedScoreB := baseImportance * 0.71653131057
		assert.InDelta(t, expectedScoreB, scoreB, 0.001, "High strength memory should decay slower matching formula")

		// Verify magnitude of difference
		// B should be significantly higher. exp(-0.33) vs exp(-1.0) is ~0.71 vs ~0.36
		assert.Greater(t, scoreB, scoreA*1.5, "High strength memory should be at least 50% stronger than standard after 24h")
	})
}
