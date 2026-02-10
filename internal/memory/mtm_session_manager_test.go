package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

func TestSessionManager_calculateMergeConfidence(t *testing.T) {
	sm := NewSessionManager(nil, nil)
	now := time.Now()

	testCases := []struct {
		name             string
		continuityResult *ContinuityResult
		existingChain    *models.CognitiveChain
		newChain         *models.CognitiveChain
		expected         float64
	}{
		{
			name:             "Base Case",
			continuityResult: &ContinuityResult{Confidence: 0.7},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-2 * time.Hour), EventCount: 5},
			newChain:         &models.CognitiveChain{ChainID: "chain2", StartedAt: now, EventCount: 5},
			expected:         0.75, // 0.7 + 0.05 (time boost for < 6 hours)
		},
		{
			name:             "Same ChainID Boost",
			continuityResult: &ContinuityResult{Confidence: 0.7},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-2 * time.Hour), EventCount: 5},
			newChain:         &models.CognitiveChain{ChainID: "chain1", StartedAt: now, EventCount: 5}, // Same ChainID
			expected:         0.95,                                                                     // 0.7 + 0.2 (same chainID) + 0.05 (time boost)
		},
		{
			name:             "Recent Time Boost (under 1 hour)",
			continuityResult: &ContinuityResult{Confidence: 0.7},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-30 * time.Minute), EventCount: 5},
			newChain:         &models.CognitiveChain{ChainID: "chain2", StartedAt: now, EventCount: 5},
			expected:         0.8, // 0.7 + 0.1
		},
		{
			name:             "Time Boost (under 6 hours)",
			continuityResult: &ContinuityResult{Confidence: 0.7},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-5 * time.Hour), EventCount: 5},
			newChain:         &models.CognitiveChain{ChainID: "chain2", StartedAt: now, EventCount: 5},
			expected:         0.75, // 0.7 + 0.05
		},
		{
			name:             "Size Penalty (large difference)",
			continuityResult: &ContinuityResult{Confidence: 0.7},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-2 * time.Hour), EventCount: 20},
			newChain:         &models.CognitiveChain{ChainID: "chain2", StartedAt: now, EventCount: 5}, // EventCount diff > 10
			expected:         0.65,                                                                     // 0.7 + 0.05 (time boost) - 0.1 (size penalty)
		},
		{
			name:             "Confidence Capping at 1.0",
			continuityResult: &ContinuityResult{Confidence: 0.9},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-2 * time.Hour), EventCount: 5},
			newChain:         &models.CognitiveChain{ChainID: "chain1", StartedAt: now, EventCount: 5}, // 0.9 + 0.2 = 1.1
			expected:         1.0,                                                                      // Should be capped at 1.0
		},
		{
			name:             "Confidence Floor at 0.0",
			continuityResult: &ContinuityResult{Confidence: 0.05},
			existingChain:    &models.CognitiveChain{ChainID: "chain1", StartedAt: now.Add(-2 * time.Hour), EventCount: 20},
			newChain:         &models.CognitiveChain{ChainID: "chain2", StartedAt: now, EventCount: 5}, // 0.05 - 0.1 = -0.05
			expected:         0.0,                                                                      // Should be floored at 0.0
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			confidence := sm.calculateMergeConfidence(tc.continuityResult, tc.existingChain, tc.newChain)
			// Use tolerance for floating point comparison
			tolerance := 0.001
			if confidence < tc.expected-tolerance || confidence > tc.expected+tolerance {
				t.Errorf("Expected confidence %.2f, but got %.2f", tc.expected, confidence)
			}
		})
	}
}

func TestSessionManager_analyzeMergeDecision(t *testing.T) {
	sm := NewSessionManager(nil, nil)
	// Override config for predictable thresholds
	sm.config.MergeMinConfidence = 0.7
	sm.config.SimilarityThreshold = 0.6

	testCases := []struct {
		name           string
		candidates     []*ChainMergeCandidate
		shouldMerge    bool
		expectedReason string
	}{
		{
			name: "Case A: Should Merge",
			candidates: []*ChainMergeCandidate{
				{
					MergeConfidence: 0.8,
					SimilarityScore: 0.8,
					MergeReasoning:  "High similarity",
					Chain:           &models.CognitiveChain{ChainID: "target1"},
				},
			},
			shouldMerge:    true,
			expectedReason: "High confidence merge",
		},
		{
			name: "Case B: Confidence too low",
			candidates: []*ChainMergeCandidate{
				{
					MergeConfidence: 0.65, // Below 0.7 threshold
					SimilarityScore: 0.8,
					Chain:           &models.CognitiveChain{ChainID: "target2"},
				},
			},
			shouldMerge:    false,
			expectedReason: "Confidence too low",
		},
		{
			name: "Case C: Similarity too low",
			candidates: []*ChainMergeCandidate{
				{
					MergeConfidence: 0.8,
					SimilarityScore: 0.55, // Below 0.6 threshold
					Chain:           &models.CognitiveChain{ChainID: "target3"},
				},
			},
			shouldMerge:    false,
			expectedReason: "Similarity too low",
		},
		{
			name:           "No candidates",
			candidates:     []*ChainMergeCandidate{},
			shouldMerge:    false,
			expectedReason: "No candidates available",
		},
		{
			name: "Selects best candidate",
			candidates: []*ChainMergeCandidate{
				{MergeConfidence: 0.75, SimilarityScore: 0.7, Chain: &models.CognitiveChain{ChainID: "bad"}},
				{MergeConfidence: 0.85, SimilarityScore: 0.8, Chain: &models.CognitiveChain{ChainID: "good"}}, // Should be chosen
			},
			shouldMerge:    true, // Note: This test assumes candidates are pre-sorted, which the main logic does.
			expectedReason: "High confidence merge",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := sm.analyzeMergeDecision(context.Background(), &models.CognitiveChain{}, tc.candidates)
			if err != nil {
				t.Fatalf("analyzeMergeDecision failed: %v", err)
			}

			if decision.ShouldMerge != tc.shouldMerge {
				t.Errorf("Expected ShouldMerge to be %v, but got %v. Reasoning: %s", tc.shouldMerge, decision.ShouldMerge, decision.Reasoning)
			}

			if !strings.HasPrefix(decision.Reasoning, tc.expectedReason) {
				t.Errorf("Expected reasoning to start with '%s', but got '%s'", tc.expectedReason, decision.Reasoning)
			}
		})
	}
}

func TestSessionManager_combineSummaries(t *testing.T) {
	sm := NewSessionManager(nil, nil)

	testCases := []struct {
		name     string
		summary1 string
		summary2 string
		expected string
	}{
		{
			name:     "Two non-empty summaries",
			summary1: "Summary A",
			summary2: "Summary B",
			expected: "Summary A; Summary B",
		},
		{
			name:     "First summary empty",
			summary1: "",
			summary2: "Summary B",
			expected: "Summary B",
		},
		{
			name:     "Second summary empty",
			summary1: "Summary A",
			summary2: "",
			expected: "Summary A",
		},
		{
			name:     "Both summaries empty",
			summary1: "",
			summary2: "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := sm.combineSummaries(tc.summary1, tc.summary2)
			if result != tc.expected {
				t.Errorf("Expected summary '%s', but got '%s'", tc.expected, result)
			}
		})
	}
}

func TestSessionManager_mergeEntities(t *testing.T) {
	sm := NewSessionManager(nil, nil)

	tests := []struct {
		name          string
		entities1     []string
		entities2     []string
		expected      int      // Expected number of unique entities
		shouldContain []string // Entities that should be in the result
	}{
		{
			name:      "Both empty",
			entities1: []string{},
			entities2: []string{},
			expected:  0,
		},
		{
			name:          "One empty, one with entities",
			entities1:     []string{"Docker", "Kubernetes"},
			entities2:     []string{},
			expected:      2,
			shouldContain: []string{"Docker", "Kubernetes"},
		},
		{
			name:          "No duplicates",
			entities1:     []string{"Docker", "Kubernetes"},
			entities2:     []string{"Redis", "MongoDB"},
			expected:      4,
			shouldContain: []string{"Docker", "Kubernetes", "Redis", "MongoDB"},
		},
		{
			name:          "With duplicates",
			entities1:     []string{"Docker", "Kubernetes", "Redis"},
			entities2:     []string{"Redis", "MongoDB", "Docker"},
			expected:      4, // Docker, Kubernetes, Redis, MongoDB (deduplicated)
			shouldContain: []string{"Docker", "Kubernetes", "Redis", "MongoDB"},
		},
		{
			name:          "All duplicates",
			entities1:     []string{"API", "Service", "Database"},
			entities2:     []string{"API", "Service", "Database"},
			expected:      3,
			shouldContain: []string{"API", "Service", "Database"},
		},
		{
			name:          "With empty strings",
			entities1:     []string{"Docker", "", "Kubernetes"},
			entities2:     []string{"", "Redis"},
			expected:      3, // Empty strings should be filtered out
			shouldContain: []string{"Docker", "Kubernetes", "Redis"},
		},
		{
			name:          "Case sensitive deduplication",
			entities1:     []string{"Docker", "docker"},
			entities2:     []string{"DOCKER", "kubernetes"},
			expected:      4, // Should treat different cases as different entities
			shouldContain: []string{"Docker", "docker", "DOCKER", "kubernetes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sm.mergeEntities(tt.entities1, tt.entities2)

			// Check length
			if len(result) != tt.expected {
				t.Errorf("Expected %d entities, got %d. Result: %v", tt.expected, len(result), result)
			}

			// Check that expected entities are present
			resultMap := make(map[string]bool)
			for _, entity := range result {
				resultMap[entity] = true
			}

			for _, entity := range tt.shouldContain {
				if !resultMap[entity] {
					t.Errorf("Expected entity '%s' to be in result, but it wasn't. Result: %v", entity, result)
				}
			}

			// Verify no empty strings in result
			for _, entity := range result {
				if entity == "" {
					t.Error("Result should not contain empty strings")
				}
			}
		})
	}
}
