package memory

import (
	"context"
	"math"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

func TestHeatScorer_ComputeSegmentHeat_BaseImportance(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			WeightIntrinsic:    0.8,
			WeightCognitive:    0.2,
			DecayTauHours:      24.0,
			RecallGrowthFactor: 1.5,
		},
	}

	tests := []struct {
		name     string
		chain    *models.CognitiveChain
		expected float64
	}{
		{
			name: "Max importance",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 1.0,
				CognitiveScore:      1.0,
				LastAccessedAt:      futurePtr(), // DeltaT = 0
				RecallStrength:      1.0,
			},
			expected: 1.0, // 0.8*1 + 0.2*1 = 1.0
		},
		{
			name: "Zero importance (Cognitive defaults to 0.5)",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 0.0,
				CognitiveScore:      0.0, // Will be recalculated to 0.5 because DB is nil
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.1, // 0.8*0.0 + 0.2*0.5 = 0.1
		},
		{
			name: "Mixed importance",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 0.5,
				CognitiveScore:      0.5,
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.5, // 0.8*0.5 + 0.2*0.5 = 0.5
		},
		{
			name: "High Intrinsic Low Cognitive (Cognitive defaults to 0.5)",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 1.0,
				CognitiveScore:      0.0, // Will be recalculated to 0.5
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.9, // 0.8*1.0 + 0.2*0.5 = 0.9
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, _, err := scorer.ComputeSegmentHeat(context.Background(), tt.chain)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if math.Abs(score-tt.expected) > 0.001 {
				t.Errorf("Expected score %.3f, got %.3f", tt.expected, score)
			}
		})
	}
}

func TestHeatScorer_ComputeSegmentHeat_TimeDecay(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			WeightIntrinsic:    1.0, // Simplify to just test decay
			WeightCognitive:    0.0,
			DecayTauHours:      24.0,
			RecallGrowthFactor: 1.5,
		},
	}

	tests := []struct {
		name           string
		deltaHours     float64
		recallStrength float64
		expectedFactor float64 // exp(-delta / (24 * S))
	}{
		{
			name:           "No decay (now)",
			deltaHours:     0,
			recallStrength: 1.0,
			expectedFactor: 1.0,
		},
		{
			name:           "1 Tau decay (24h)",
			deltaHours:     24,
			recallStrength: 1.0,
			expectedFactor: 0.367879, // exp(-1)
		},
		{
			name:           "Spaced Repetition (24h with S=2.0)",
			deltaHours:     24,
			recallStrength: 2.0,
			expectedFactor: 0.60653, // exp(-24 / 48) = exp(-0.5)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ensure lastAccess is exactly deltaHours behind 'now' used inside?
			// Inside function uses time.Now().
			// So we need LastAccessedAt to be relative to that.
			// Since we can't control time.Now() inside, we rely on the fact that test execution is fast.
			// But for "No decay", if we put time.Now(), it might be slightly in past.
			// Using futurePtr() for "No decay" case ensures DeltaT=0.
			var lastAccess time.Time
			if tt.deltaHours == 0 {
				lastAccess = time.Now().Add(1 * time.Hour) // Future
			} else {
				lastAccess = time.Now().Add(-time.Duration(tt.deltaHours) * time.Hour)
			}

			chain := &models.CognitiveChain{
				IntrinsicImportance: 1.0,
				LastAccessedAt:      &lastAccess,
				RecallStrength:      tt.recallStrength,
			}

			score, factors, err := scorer.ComputeSegmentHeat(context.Background(), chain)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Since BaseImportance is 1.0, Score == DecayFactor
			// Relax tolerance slightly for time drift
			if math.Abs(score-tt.expectedFactor) > 0.01 {
				t.Errorf("Expected score %.4f, got %.4f", tt.expectedFactor, score)
			}

			if math.Abs(factors.TimeDecay-tt.expectedFactor) > 0.01 {
				t.Errorf("Expected factor %.4f, got %.4f", tt.expectedFactor, factors.TimeDecay)
			}
		})
	}
}

func TestHeatScorer_calculateCognitiveScore(t *testing.T) {
	scorer := &HeatScorer{db: nil} // Nil DB returns 0.5 default

	chain := &models.CognitiveChain{ChainID: "test"}
	score := scorer.calculateCognitiveScore(context.Background(), chain)

	if score != 0.5 {
		t.Errorf("Expected default score 0.5 for nil DB, got %.2f", score)
	}
}

func futurePtr() *time.Time {
	t := time.Now().Add(1 * time.Hour)
	return &t
}
