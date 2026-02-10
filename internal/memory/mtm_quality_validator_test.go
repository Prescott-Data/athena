package memory

import (
	"context"
	"testing"

	"bitbucket.org/dromos/memory-os/internal/models"
)

func TestQualityValidator_ValidateSegment_HighQuality(t *testing.T) {
	validator := NewQualityValidator()
	ctx := context.Background()

	chain := &models.CognitiveChain{
		ChainID: "test-chain-high-quality",
		Summary: "A detailed discussion about project planning and execution.",
	}

	events := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "What is the next step for the alpha release?"},
		{Role: "agent", Type: models.STMEventTypeThought, Content: "The user is asking for the project plan. I need to check the roadmap document and provide the key milestones."},
		{Role: "agent", Type: models.STMEventTypeAction, Content: "action: document_lookup, query: 'alpha release roadmap'"},
		{Role: "agent", Type: models.STMEventTypeMessage, Content: "The next step is to finalize the UI mockups by Wednesday, followed by engineering kickoff on Friday."},
	}

	result, err := validator.ValidateSegment(ctx, chain, events)
	if err != nil {
		t.Fatalf("ValidateSegment failed: %v", err)
	}

	if !result.ShouldStore {
		t.Errorf("Expected ShouldStore to be true for high-quality segment, but got false. Score: %.2f", result.QualityScore)
	}

	if !result.IsValid {
		t.Errorf("Expected IsValid to be true for high-quality segment, but got false. Score: %.2f", result.QualityScore)
	}

	t.Logf("High-quality validation passed. Score: %.3f, Recommendation: %s", result.QualityScore, result.Recommendation)
}

func TestQualityValidator_ValidateSegment_LowQuality(t *testing.T) {
	validator := NewQualityValidator()
	ctx := context.Background()

	// This chain has a non-descriptive summary and only one trivial event.
	chain := &models.CognitiveChain{
		ChainID: "test-chain-low-quality",
		Summary: "hi",
	}

	events := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "Hi"},
	}

	result, err := validator.ValidateSegment(ctx, chain, events)
	if err != nil {
		t.Fatalf("ValidateSegment failed: %v", err)
	}

	// With default "balanced" config (MinQualityScore: 0.5), this should be rejected.
	// The test relies on the scoring logic correctly identifying this as low value.
	// Note: If the dummy scoring logic in the validator is too generous, this test may fail,
	// correctly indicating a flaw in the validator's logic.
	if result.ShouldStore {
		t.Errorf("Expected ShouldStore to be false for low-quality segment, but got true. Score: %.2f", result.QualityScore)
	}

	if len(result.Metrics.QualityIssues) == 0 {
		t.Error("Expected to find quality issues for a low-quality segment, but none were found.")
	}

	t.Logf("Low-quality validation passed. Score: %.3f, Issues: %v", result.QualityScore, result.Metrics.QualityIssues)
}

func TestQualityValidator_calculateTotalWordCount(t *testing.T) {
	validator := NewQualityValidator()
	events := []models.CognitiveEvent{
		{Content: "This is a test."},
		{Content: "Here is another one."},
		{Content: ""}, // Empty content
		{Content: "Final."},
	}

	expected := 4 + 4 + 1
	actual := validator.calculateTotalWordCount(events)

	if actual != expected {
		t.Errorf("calculateTotalWordCount() = %d, want %d", actual, expected)
	}
}

func TestQualityValidator_hasUserQuestions(t *testing.T) {
	validator := NewQualityValidator()

	tests := []struct {
		name     string
		events   []models.CognitiveEvent
		expected bool
	}{
		{
			name: "with question mark",
			events: []models.CognitiveEvent{
				{Role: "user", Content: "What is the status?"},
				{Role: "agent", Content: "It is pending."},
			},
			expected: true,
		},
		{
			name: "with 'how' prefix",
			events: []models.CognitiveEvent{
				{Role: "user", Content: "how do I reset my password"},
				{Role: "agent", Content: "Go to settings."},
			},
			expected: true,
		},
		{
			name: "no question",
			events: []models.CognitiveEvent{
				{Role: "user", Content: "That makes sense."},
				{Role: "agent", Content: "Great."},
			},
			expected: false,
		},
		{
			name: "question from agent",
			events: []models.CognitiveEvent{
				{Role: "user", Content: "Okay."},
				{Role: "agent", Content: "Are you sure?"},
			},
			expected: false, // Should only detect questions from the user
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := validator.hasUserQuestions(tt.events)
			if actual != tt.expected {
				t.Errorf("hasUserQuestions() = %v, want %v", actual, tt.expected)
			}
		})
	}
}

func TestQualityValidator_CognitiveDepthScoring(t *testing.T) {
	validator := NewQualityValidator()
	ctx := context.Background()

	tests := []struct {
		name                  string
		events                []models.CognitiveEvent
		expectedHasThoughts   bool
		expectedHasActions    bool
		expectedMinDepthScore float64
		description           string
	}{
		{
			name: "No thoughts or actions",
			events: []models.CognitiveEvent{
				{Role: "user", Type: models.STMEventTypeMessage, Content: "Hello"},
				{Role: "agent", Type: models.STMEventTypeMessage, Content: "Hi there"},
			},
			expectedHasThoughts:   false,
			expectedHasActions:    false,
			expectedMinDepthScore: 0.5, // Base score
			description:           "Simple message exchange should have base cognitive depth",
		},
		{
			name: "With thought events",
			events: []models.CognitiveEvent{
				{Role: "user", Type: models.STMEventTypeMessage, Content: "What's 2+2?"},
				{Role: "agent", Type: models.STMEventTypeThought, Content: "The user is asking a simple math question"},
				{Role: "agent", Type: models.STMEventTypeMessage, Content: "2+2 equals 4"},
			},
			expectedHasThoughts:   true,
			expectedHasActions:    false,
			expectedMinDepthScore: 0.56, // Base (0.5) + thought bonus (0.2 * 1/3 = 0.067) = 0.567
			description:           "Chain with thoughts should score higher than base",
		},
		{
			name: "With action events",
			events: []models.CognitiveEvent{
				{Role: "user", Type: models.STMEventTypeMessage, Content: "Check the weather"},
				{Role: "agent", Type: models.STMEventTypeAction, Content: "action: weather_api_call"},
				{Role: "agent", Type: models.STMEventTypeMessage, Content: "It's sunny today"},
			},
			expectedHasThoughts:   false,
			expectedHasActions:    true,
			expectedMinDepthScore: 0.54, // Base (0.5) + action bonus (0.15 * 1/3 = 0.05) = 0.55
			description:           "Chain with actions should score higher than base",
		},
		{
			name: "With both thoughts and actions",
			events: []models.CognitiveEvent{
				{Role: "user", Type: models.STMEventTypeMessage, Content: "Find me a document about AI"},
				{Role: "agent", Type: models.STMEventTypeThought, Content: "User needs document search, I should use the document API"},
				{Role: "agent", Type: models.STMEventTypeAction, Content: "action: document_search, query: 'AI'"},
				{Role: "agent", Type: models.STMEventTypeObservation, Content: "Found 5 documents"},
				{Role: "agent", Type: models.STMEventTypeMessage, Content: "I found 5 documents about AI"},
			},
			expectedHasThoughts:   true,
			expectedHasActions:    true,
			expectedMinDepthScore: 0.7, // Base + thought + action + balance bonus
			description:           "Chain with both thoughts and actions should score highest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain := &models.CognitiveChain{
				ChainID: "test-chain-" + tt.name,
				Summary: "Test conversation for cognitive depth",
			}

			result, err := validator.ValidateSegment(ctx, chain, tt.events)
			if err != nil {
				t.Fatalf("ValidateSegment failed: %v", err)
			}

			// Check cognitive depth metrics
			if result.Metrics.HasThoughts != tt.expectedHasThoughts {
				t.Errorf("Expected HasThoughts=%v, got %v", tt.expectedHasThoughts, result.Metrics.HasThoughts)
			}

			if result.Metrics.HasActions != tt.expectedHasActions {
				t.Errorf("Expected HasActions=%v, got %v", tt.expectedHasActions, result.Metrics.HasActions)
			}

			if result.Metrics.CognitiveDepthScore < tt.expectedMinDepthScore {
				t.Errorf("Expected CognitiveDepthScore >= %.2f, got %.2f", tt.expectedMinDepthScore, result.Metrics.CognitiveDepthScore)
			}

			// Log for debugging
			t.Logf("%s: ThoughtCount=%d, ActionCount=%d, DepthScore=%.3f",
				tt.description, result.Metrics.ThoughtCount, result.Metrics.ActionCount, result.Metrics.CognitiveDepthScore)
		})
	}
}

func TestQualityValidator_analyzeCognitiveEventTypes(t *testing.T) {
	validator := NewQualityValidator()

	tests := []struct {
		name             string
		events           []models.CognitiveEvent
		expectedThoughts int
		expectedActions  int
	}{
		{
			name: "No cognitive events",
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeMessage, Content: "Hello"},
			},
			expectedThoughts: 0,
			expectedActions:  0,
		},
		{
			name: "Multiple thoughts",
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeThought, Content: "Thought 1"},
				{Type: models.STMEventTypeThought, Content: "Thought 2"},
				{Type: models.STMEventTypeMessage, Content: "Message"},
			},
			expectedThoughts: 2,
			expectedActions:  0,
		},
		{
			name: "Multiple actions",
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeAction, Content: "Action 1"},
				{Type: models.STMEventTypeAction, Content: "Action 2"},
				{Type: models.STMEventTypeAction, Content: "Action 3"},
			},
			expectedThoughts: 0,
			expectedActions:  3,
		},
		{
			name: "Mixed cognitive events",
			events: []models.CognitiveEvent{
				{Type: models.STMEventTypeThought, Content: "Thinking"},
				{Type: models.STMEventTypeAction, Content: "Acting"},
				{Type: models.STMEventTypeMessage, Content: "Message"},
				{Type: models.STMEventTypeThought, Content: "More thinking"},
			},
			expectedThoughts: 2,
			expectedActions:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thoughts, actions := validator.analyzeCognitiveEventTypes(tt.events)

			if thoughts != tt.expectedThoughts {
				t.Errorf("Expected %d thoughts, got %d", tt.expectedThoughts, thoughts)
			}

			if actions != tt.expectedActions {
				t.Errorf("Expected %d actions, got %d", tt.expectedActions, actions)
			}
		})
	}
}
