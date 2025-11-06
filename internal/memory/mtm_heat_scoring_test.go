package memory

import (
	"context"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestHeatScorer_calculateAccessFrequency(t *testing.T) {
	scorer := &HeatScorer{}

	tests := []struct {
		name        string
		segment     *models.Segment
		expected    float64
		description string
	}{
		{
			name: "no access",
			segment: &models.Segment{
				AccessCount: 0,
			},
			expected:    0.0,
			description: "Segment with no access should have 0 frequency score",
		},
		{
			name: "single access",
			segment: &models.Segment{
				AccessCount: 1,
			},
			expected:    0.693147, // log(1 + 1) ≈ 0.693
			description: "Single access should use logarithmic scaling",
		},
		{
			name: "multiple accesses",
			segment: &models.Segment{
				AccessCount: 9, // log(10) ≈ 2.303
			},
			expected:    2.302585,
			description: "Multiple accesses should scale logarithmically",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateAccessFrequency(tt.segment)
			if absFloat(result-tt.expected) > 0.001 { // Allow small floating point differences
				t.Errorf("calculateAccessFrequency() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateInteractionDepth(t *testing.T) {
	scorer := &HeatScorer{}
	ctx := context.Background()

	tests := []struct {
		name        string
		segment     *models.Segment
		expected    float64
		description string
	}{
		{
			name: "no pages",
			segment: &models.Segment{
				PageIDs: []primitive.ObjectID{},
			},
			expected:    0.0,
			description: "Segment with no pages should have 0 depth score",
		},
		{
			name: "single page",
			segment: &models.Segment{
				PageIDs: []primitive.ObjectID{primitive.NewObjectID()},
			},
			expected:    0.693147, // log(1 + 1) ≈ 0.693
			description: "Single page should use logarithmic scaling",
		},
		{
			name: "stored interaction size",
			segment: &models.Segment{
				PageIDs:         []primitive.ObjectID{primitive.NewObjectID()},
				InteractionSize: 5,
			},
			expected:    1.791759, // log(5 + 1) ≈ 1.792
			description: "Should use stored interaction size when available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateInteractionDepth(ctx, tt.segment)
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
		segment     *models.Segment
		currentTime time.Time
		expected    float64
		description string
	}{
		{
			name: "just accessed",
			segment: &models.Segment{
				CreatedAt:      now,
				LastAccessTime: &now,
			},
			currentTime: now,
			expected:    1.0, // e^(-0/24) = 1
			description: "Just accessed segment should have maximum recency",
		},
		{
			name: "one day old",
			segment: &models.Segment{
				CreatedAt:      now.Add(-24 * time.Hour),
				LastAccessTime: nil, // Will use CreatedAt
			},
			currentTime: now,
			expected:    0.367879, // e^(-24/24) = e^(-1) ≈ 0.368
			description: "One day old segment should have exponential decay",
		},
		{
			name: "very old",
			segment: &models.Segment{
				CreatedAt: now.Add(-200 * time.Hour), // Older than max age
			},
			currentTime: now,
			expected:    0.001, // Below max age threshold
			description: "Very old segment should have minimal recency score",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateRecencyScore(tt.segment, tt.currentTime)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateRecencyScore() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_calculateUserEngagement(t *testing.T) {
	scorer := &HeatScorer{}
	ctx := context.Background()

	tests := []struct {
		name        string
		segment     *models.Segment
		expected    float64
		description string
	}{
		{
			name: "no pages",
			segment: &models.Segment{
				PageIDs: []primitive.ObjectID{},
			},
			expected:    0.0,
			description: "Segment with no pages should have 0 engagement",
		},
		{
			name: "simple engagement",
			segment: &models.Segment{
				PageIDs:      []primitive.ObjectID{primitive.NewObjectID()},
				TopicSummary: "Short summary",
			},
			expected:    0.22702358, // tanh(log(2)/3) ≈ 0.227
			description: "Single page with short summary",
		},
		{
			name: "high engagement",
			segment: &models.Segment{
				PageIDs: []primitive.ObjectID{
					primitive.NewObjectID(),
					primitive.NewObjectID(),
					primitive.NewObjectID(),
				},
				TopicSummary: "This is a very long summary that indicates a complex and detailed conversation with multiple exchanges between the user and assistant about various topics",
			},
			expected:    0.5038,
			description: "Multiple pages with long summary should have higher engagement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateUserEngagement(ctx, tt.segment)
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
		segment     *models.Segment
		expected    float64
		description string
	}{
		{
			name: "no important keywords",
			segment: &models.Segment{
				TopicSummary: "Just a normal conversation about weather",
			},
			expected:    0.462117, // tanh(0.5) ≈ 0.462
			description: "Normal topic should have default importance",
		},
		{
			name: "urgent topic",
			segment: &models.Segment{
				TopicSummary: "Urgent problem that needs immediate help",
			},
			expected:    0.66403, // tanh(0.5 + 0.1 * 3) = tanh(0.8) ≈ 0.664
			description: "Urgent topic should have higher importance",
		},
		{
			name: "multiple important keywords",
			segment: &models.Segment{
				TopicSummary: "Critical error causing urgent issue that needs help",
			},
			expected:    0.76159, // tanh(0.5 + 0.1 * 5) = tanh(1.0) ≈ 0.761
			description: "Multiple important keywords should boost importance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scorer.calculateTopicImportance(tt.segment)
			if absFloat(result-tt.expected) > 0.001 {
				t.Errorf("calculateTopicImportance() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHeatScorer_ComputeSegmentHeat_Integration(t *testing.T) {
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

	// Test segment with moderate activity
	segment := &models.Segment{
		SegmentID:       "test_segment_1",
		AccessCount:     5, // Good access frequency
		InteractionSize: 3, // Moderate interaction depth
		PageIDs: []primitive.ObjectID{
			primitive.NewObjectID(),
			primitive.NewObjectID(),
			primitive.NewObjectID(),
		},
		TopicSummary:   "Discussion about important project deadline",
		CreatedAt:      now.Add(-12 * time.Hour),                                        // 12 hours ago
		LastAccessTime: func() *time.Time { t := now.Add(-2 * time.Hour); return &t }(), // 2 hours ago
	}

	heatScore, heatFactors, err := scorer.ComputeSegmentHeat(ctx, segment)

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

func TestHeatScorer_ComputeSegmentHeat_EdgeCases(t *testing.T) {
	scorer := NewHeatScorer(nil) // No database for unit tests
	ctx := context.Background()

	tests := []struct {
		name        string
		segment     *models.Segment
		expectError bool
		description string
	}{
		{
			name: "empty segment",
			segment: &models.Segment{
				CreatedAt: time.Now(),
			},
			expectError: false,
			description: "Empty segment should not cause errors",
		},
		{
			name: "zero access count",
			segment: &models.Segment{
				SegmentID:    "test_zero_access",
				AccessCount:  0,
				CreatedAt:    time.Now(),
				TopicSummary: "Test summary",
			},
			expectError: false,
			description: "Zero access count should be handled gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			heatScore, heatFactors, err := scorer.ComputeSegmentHeat(ctx, tt.segment)

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

// Helper function for floating point comparison
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
