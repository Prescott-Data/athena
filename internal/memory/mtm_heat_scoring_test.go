package memory

import (
	"context"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

func TestHeatScorer_calculateAccessFrequency(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expected    float64
		description string
	}{
		{
			name: "no access",
			chain: &models.CognitiveChain{
				AccessCount: 0,
			},
			expected:    0.0,
			description: "Chain with no access should have 0 frequency score",
		},
		{
			name: "single access",
			chain: &models.CognitiveChain{
				AccessCount: 1,
			},
			expected:    0.693147, // log(1 + 1) ≈ 0.693
			description: "Single access should use logarithmic scaling",
		},
		{
			name: "multiple accesses",
			chain: &models.CognitiveChain{
				AccessCount: 9, // log(10) ≈ 2.303
			},
			expected:    2.302585,
			description: "Multiple accesses should scale logarithmically",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateAccessFrequency(tt.chain)
			if absFloat(result-tt.expected) > 0.001 { // Allow small floating point differences
				t.Errorf("calculateAccessFrequency() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateInteractionDepth(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expected    float64
		description string
	}{
		{
			name: "no events",
			chain: &models.CognitiveChain{
				EventCount: 0,
			},
			expected:    0.0,
			description: "Chain with no events should have 0 depth score",
		},
		{
			name: "single event",
			chain: &models.CognitiveChain{
				EventCount: 1,
			},
			expected:    0.693147, // log(1 + 1) ≈ 0.693
			description: "Single event should use logarithmic scaling",
		},
		{
			name: "multiple events",
			chain: &models.CognitiveChain{
				EventCount: 5,
			},
			expected:    1.791759, // log(5 + 1) ≈ 1.792
			description: "Should use event count for interaction depth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateInteractionDepth(tt.chain)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateInteractionDepth() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateRecencyScore(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			RecencyTauHours: 24.0,
			RecencyMaxAge:   168.0, // 1 week
		},
	}

	now := time.Now()

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		currentTime time.Time
		expected    float64
		description string
	}{
		{
			name: "just occurred",
			chain: &models.CognitiveChain{
				StartedAt:   now,
				LastEventAt: now,
			},
			currentTime: now,
			expected:    1.0, // e^(-0/24) = 1
			description: "Just occurred chain should have maximum recency",
		},
		{
			name: "one day old",
			chain: &models.CognitiveChain{
				StartedAt: now.Add(-24 * time.Hour),
				// LastEventAt is zero, will use StartedAt
			},
			currentTime: now,
			expected:    0.367879, // e^(-24/24) = e^(-1) ≈ 0.368
			description: "One day old chain should have exponential decay",
		},
		{
			name: "very old",
			chain: &models.CognitiveChain{
				StartedAt: now.Add(-200 * time.Hour), // Older than max age
			},
			currentTime: now,
			expected:    0.001, // Below max age threshold
			description: "Very old chain should have minimal recency score",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateRecencyScore(tt.chain, tt.currentTime)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateRecencyScore() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateUserEngagement(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expected    float64
		description string
	}{
		{
			name: "no events",
			chain: &models.CognitiveChain{
				EventCount: 0,
			},
			expected:    0.0,
			description: "Chain with no events should have 0 engagement",
		},
		{
			name: "simple engagement",
			chain: &models.CognitiveChain{
				EventCount: 1,
				Summary:    "Short summary",
			},
			expected:    0.22702358, // tanh(log(2)/3) ≈ 0.227
			description: "Single event with short summary",
		},
		{
			name: "high engagement",
			chain: &models.CognitiveChain{
				EventCount: 3,
				Summary:    "This is a very long summary that indicates a complex and detailed conversation with multiple exchanges between the user and assistant about various topics",
			},
			expected:    0.5038,
			description: "Multiple events with long summary should have higher engagement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateUserEngagement(tt.chain)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateUserEngagement() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateTopicImportance(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expected    float64
		description string
	}{
		{
			name: "no important keywords",
			chain: &models.CognitiveChain{
				Summary: "Just a normal conversation about weather",
			},
			expected:    0.462117, // tanh(0.5) ≈ 0.462
			description: "Normal topic should have default importance",
		},
		{
			name: "urgent topic",
			chain: &models.CognitiveChain{
				Summary: "Urgent problem that needs immediate help",
			},
			expected:    0.66403, // tanh(0.5 + 0.1 * 3) = tanh(0.8) ≈ 0.664
			description: "Urgent topic should have higher importance",
		},
		{
			name: "multiple important keywords",
			chain: &models.CognitiveChain{
				Summary: "Critical error causing urgent issue that needs help",
			},
			expected:    0.76159, // tanh(0.5 + 0.1 * 5) = tanh(1.0) ≈ 0.761
			description: "Multiple important keywords should boost importance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateTopicImportance(tt.chain)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateTopicImportance() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_ComputeChainHeat_Integration(t *testing.T) {
	scorer := &HeatScorer{
		config: HeatScoringConfig{
			Alpha:           1.0, // Access frequency weight
			Beta:            1.0, // Interaction depth weight
			Gamma:           1.0, // Recency weight
			Delta:           0.5, // User engagement weight
			Epsilon:         0.3, // Topic importance weight
			RecencyTauHours: 24.0,
			RecencyMaxAge:   168.0,
		},
	}

	ctx := context.Background()
	now := time.Now()

	// Test chain with moderate activity
	chain := &models.CognitiveChain{
		ChainID:        "test_chain_1",
		AccessCount:    5, // Good access frequency
		EventCount:     3, // Moderate interaction depth
		Summary:        "Discussion about important project deadline",
		StartedAt:      now.Add(-12 * time.Hour),                                        // 12 hours ago
		LastEventAt:    now.Add(-2 * time.Hour),                                         // 2 hours ago
		LastAccessTime: func() *time.Time { t := now.Add(-2 * time.Hour); return &t }(), // 2 hours ago
	}

	heatScore, heatFactors, err := scorer.ComputeSegmentHeat(ctx, chain)

	if err != nil {
		t.Fatalf("ComputeSegmentHeat() error = %v", err)
	}

	// Check that heat score is reasonable (should be positive and normalized)
	if heatScore <= 0 || heatScore > 1 {
		t.Errorf("ComputeSegmentHeat() heatScore = %v, want (0, 1]", heatScore)
	}

	// Check that heat factors are populated
	if heatFactors == nil {
		t.Fatal("ComputeSegmentHeat() heatFactors should not be nil")
	}

	// Check individual factors are reasonable
	if heatFactors.AccessFrequency <= 0 {
		t.Errorf("AccessFrequency should be positive, got %v", heatFactors.AccessFrequency)
	}

	if heatFactors.InteractionDepth <= 0 {
		t.Errorf("InteractionDepth should be positive, got %v", heatFactors.InteractionDepth)
	}

	if heatFactors.RecencyScore <= 0 || heatFactors.RecencyScore > 1 {
		t.Errorf("RecencyScore should be in (0, 1], got %v", heatFactors.RecencyScore)
	}

	t.Logf("Heat Score: %.3f", heatScore)
	t.Logf("Factors - Access: %.3f, Depth: %.3f, Recency: %.3f, Engagement: %.3f, Importance: %.3f",
		heatFactors.AccessFrequency, heatFactors.InteractionDepth, heatFactors.RecencyScore,
		heatFactors.UserEngagement, heatFactors.TopicImportance)
}

func TestHeatScorer_ComputeChainHeat_EdgeCases(t *testing.T) {
	scorer := NewHeatScorer(nil) // No database for unit tests
	ctx := context.Background()

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expectError bool
		description string
	}{
		{
			name: "empty chain",
			chain: &models.CognitiveChain{
				StartedAt: time.Now(),
			},
			expectError: false,
			description: "Empty chain should not cause errors",
		},
		{
			name: "zero access count",
			chain: &models.CognitiveChain{
				ChainID:     "test_zero_access",
				AccessCount: 0,
				StartedAt:   time.Now(),
				Summary:     "Test summary",
			},
			expectError: false,
			description: "Zero access count should be handled gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			heatScore, heatFactors, err := scorer.ComputeSegmentHeat(ctx, tt.chain)

			if tt.expectError && err == nil {
				t.Errorf("ComputeSegmentHeat() expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("ComputeSegmentHeat() unexpected error = %v", err)
			}

			if !tt.expectError {
				// Check basic constraints
				if heatScore < 0 || heatScore > 1 {
					t.Errorf("Heat score %v outside expected range [0, 1]", heatScore)
				}

				if heatFactors == nil {
					t.Error("Heat factors should not be nil for successful computation")
				}
			}
		})
	}
}

func TestHeatScorer_calculateCognitiveDepth(t *testing.T) {
	scorer := &HeatScorer{
		db: nil, // Will test nil database handling
	}
	ctx := context.Background()

	tests := []struct {
		name        string
		chain       *models.CognitiveChain
		expected    float64
		description string
	}{
		{
			name: "nil database returns default",
			chain: &models.CognitiveChain{
				ChainID:    "test-chain-1",
				EventCount: 5,
			},
			expected:    0.5,
			description: "Should return default 0.5 when database is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateCognitiveDepth(ctx, tt.chain)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateCognitiveDepth() = %v, want %v", result, tt.expected)
			}
			t.Logf("%s: CognitiveDepth=%.3f", tt.description, result)
		})
	}
}

func TestHeatScorer_ComputeSegmentHeat_WithCognitiveDepth(t *testing.T) {
	scorer := NewHeatScorer(nil) // Nil DB will use default cognitive depth
	ctx := context.Background()

	chain := &models.CognitiveChain{
		ChainID:     "test-chain-cognitive",
		UserID:      "user1",
		Summary:     "Complex problem solving with tool usage",
		AccessCount: 3,
		EventCount:  5,
		StartedAt:   time.Now().Add(-1 * time.Hour),
		LastEventAt: time.Now().Add(-30 * time.Minute),
	}

	score, factors, err := scorer.ComputeSegmentHeat(ctx, chain)
	if err != nil {
		t.Fatalf("ComputeSegmentHeat failed: %v", err)
	}

	// Verify cognitive depth factor is included
	if factors.CognitiveDepth == 0.0 {
		t.Error("CognitiveDepth factor should not be zero")
	}

	// Cognitive depth should contribute to overall score
	if score <= 0.0 {
		t.Errorf("Heat score should be positive, got %.3f", score)
	}

	t.Logf("Heat Score: %.3f, Factors: AccessFreq=%.3f, Depth=%.3f, Recency=%.3f, Engagement=%.3f, TopicImp=%.3f, CogDepth=%.3f",
		score, factors.AccessFrequency, factors.InteractionDepth, factors.RecencyScore,
		factors.UserEngagement, factors.TopicImportance, factors.CognitiveDepth)
}

// Helper function for floating point comparison
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
