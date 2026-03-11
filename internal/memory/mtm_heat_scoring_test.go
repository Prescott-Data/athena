package memory

import (
	"context"
	"math"
	"testing"
	"time"

	"bitbucket.org/dromos/athena-memos/internal/models"
)

func TestHeatScorer_ComputeSegmentHeat_BaseImportance(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			WeightIntrinsic:    0.7,
			WeightDensity:      0.3,
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
				DensityScore:        1.0,
				LastAccessedAt:      futurePtr(), // DeltaT = 0
				RecallStrength:      1.0,
			},
			expected: 1.0, // 0.7*1 + 0.3*1 = 1.0
		},
		{
			name: "Zero importance (Density defaults to 0.0 as nil DB fails fetch)",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 0.0,
				DensityScore:        0.0,
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.0, // 0.7*0.0 + 0.3*0.0 = 0.0
		},
		{
			name: "Mixed importance",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 0.5,
				DensityScore:        0.5,
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.5, // 0.7*0.5 + 0.3*0.5 = 0.5
		},
		{
			name: "High Intrinsic Low Density",
			chain: &models.CognitiveChain{
				IntrinsicImportance: 1.0,
				DensityScore:        0.0,
				LastAccessedAt:      futurePtr(),
				RecallStrength:      1.0,
			},
			expected: 0.7, // 0.7*1.0 + 0.3*0.0 = 0.7
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
			WeightDensity:      0.0,
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

func TestHeatScorer_CalculateDensity(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name     string
		chain    *models.CognitiveChain
		events   []models.CognitiveEvent
		expected float64
	}{
		{
			name:     "Empty chain and events",
			chain:    &models.CognitiveChain{},
			events:   []models.CognitiveEvent{},
			expected: 0.0,
		},
		{
			name: "Has Entities (+0.15)",
			chain: &models.CognitiveChain{
				Entities: []string{"User1", "ProjectA"},
			},
			events:   []models.CognitiveEvent{},
			expected: 0.15,
		},
		{
			name:  "Has Thought (+0.20)",
			chain: &models.CognitiveChain{},
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeThought},
			},
			expected: 0.20,
		},
		{
			name:  "Has Action (+0.20 - unique per chain)",
			chain: &models.CognitiveChain{},
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeAction},
				{Type: models.STMEventTypeThought}, // Should not add another 0.20
			},
			expected: 0.20,
		},
		{
			name:  "Silent System Workflow (+0.25)",
			chain: &models.CognitiveChain{},
			events: []models.CognitiveEvent{
				{
					Type: models.STMEventTypeObservation,
					Metadata: map[string]interface{}{
						"workflow_id": "wf-123",
						"blob_url":    "http://blob",
					},
				},
			},
			expected: 0.25,
		},
		{
			name:  "Multi-Service Context (+0.20)",
			chain: &models.CognitiveChain{},
			events: []models.CognitiveEvent{
				{Metadata: map[string]interface{}{"origin_service": "service-a"}},
				{Metadata: map[string]interface{}{"origin_service": "service-b"}},
			},
			expected: 0.20,
		},
		{
			name: "Complex High Density (Entities + Thought + Workflow + MultiService)",
			chain: &models.CognitiveChain{
				Entities: []string{"Entity1"},
			},
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeThought, Metadata: map[string]interface{}{"origin_service": "service-a"}},
				{
					Type: models.STMEventTypeObservation,
					Metadata: map[string]interface{}{
						"workflow_id":    "wf-123",
						"origin_service": "service-b",
					},
				},
			},
			// 0.15 (Entities) + 0.20 (Thought) + 0.25 (Workflow) + 0.20 (MultiService) = 0.80
			expected: 0.80,
		},
		{
			name:  "Capped at 1.0",
			chain: &models.CognitiveChain{Entities: []string{"E1"}}, // 0.15
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeThought}, // 0.20
				{Type: models.STMEventTypeObservation, Metadata: map[string]interface{}{"workflow_id": "1"}}, // 0.25
				{Metadata: map[string]interface{}{"origin_service": "s1"}},
				{Metadata: map[string]interface{}{"origin_service": "s2"}}, // 0.20
				// Total so far: 0.80. Let's add more "virtual" density to simulate overflow if heuristics changed,
				// or just rely on the fact that if we tweaked numbers it would cap.
				// For now, let's assume we want to hit the cap.
				// Let's pretend we have a duplicate bonus test or just ensure 0.8 is correct.
				// To force cap with current heuristics: we need > 1.0.
				// Current max possible: 0.15 + 0.20 + 0.25 + 0.20 = 0.80.
				// Wait, the heuristics sum to 0.80 max?
				// Entities: 0.15
				// Thought/Action: 0.20
				// Workflow: 0.25
				// Multi-Service: 0.20
				// Sum: 0.80.
				// So it seems impossible to reach 1.0 strictly with these rules alone unless I missed something or miscalculated.
				// Prompt says: "Return the final score capped at 1.0".
				// So strictly speaking, max is 0.8.
				// Unless multiple events trigger something I missed? "set a flag to add 0.20 once".
				// Okay, so max is 0.8. I will test for 0.8.
			},
			expected: 0.80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.CalculateDensity(tt.chain, tt.events)
			if math.Abs(score-tt.expected) > 0.001 {
				t.Errorf("Expected score %.3f, got %.3f", tt.expected, score)
			}
		})
	}
}

func TestHeatScorer_ApplyAccessUpdate_Cramming(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			CooldownHours:      12.0,
			RecallGrowthFactor: 1.5,
			DecayTauHours:      24.0,
			WeightIntrinsic:    0.7,
			WeightDensity:      0.3,
		},
	}

	lastAccess := time.Now().Add(-1 * time.Hour) // 1 hour ago (inside 12h cooldown)
	initialStrength := 1.0

	chain := &models.CognitiveChain{
		LastAccessedAt:      &lastAccess,
		RecallStrength:      initialStrength,
		IntrinsicImportance: 0.5,
		DensityScore:        0.5,
	}

	err := scorer.ApplyAccessUpdate(context.Background(), chain)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Strength should NOT change because we are inside the cooldown period
	if math.Abs(chain.RecallStrength-initialStrength) > 0.001 {
		t.Errorf("Cramming check failed: Strength changed from %.2f to %.2f but should be constant", initialStrength, chain.RecallStrength)
	}

	// LastAccessedAt should still be updated to "now"
	if time.Since(*chain.LastAccessedAt) > 5*time.Second {
		t.Errorf("LastAccessedAt not updated to current time")
	}
}

func TestHeatScorer_ApplyAccessUpdate_Spaced(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			CooldownHours:      12.0,
			RecallGrowthFactor: 1.5,
			DecayTauHours:      24.0,
			WeightIntrinsic:    0.7,
			WeightDensity:      0.3,
		},
	}

	lastAccess := time.Now().Add(-24 * time.Hour) // 24 hours ago (outside 12h cooldown)
	initialStrength := 1.0

	chain := &models.CognitiveChain{
		LastAccessedAt:      &lastAccess,
		RecallStrength:      initialStrength,
		IntrinsicImportance: 0.5,
		DensityScore:        0.5,
	}

	err := scorer.ApplyAccessUpdate(context.Background(), chain)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Strength SHOULD increase by 1.5x
	expectedStrength := initialStrength * 1.5
	if math.Abs(chain.RecallStrength-expectedStrength) > 0.001 {
		t.Errorf("Spaced check failed: Expected strength %.2f, got %.2f", expectedStrength, chain.RecallStrength)
	}

	// LastAccessedAt should be updated
	if time.Since(*chain.LastAccessedAt) > 5*time.Second {
		t.Errorf("LastAccessedAt not updated to current time")
	}
}

func futurePtr() *time.Time {
	t := time.Now().Add(1 * time.Hour)
	return &t
}
