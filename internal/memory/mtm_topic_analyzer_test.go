package memory

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"bitbucket.org/dromos/memory-os/internal/models"
)

// --- Test Cases ---

func TestTopicAnalyzer_buildConversationText(t *testing.T) {
	// 1. Setup
	analyzer := NewTopicAnalyzer(nil, nil)
	events := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "Hello, can you help me?"},
		{Role: "agent", Type: models.STMEventTypeThought, Content: "User needs assistance. I should ask for clarification."},
		{Role: "agent", Type: models.STMEventTypeAction, Content: "action: clarify_request"},
		{Role: "agent", Type: models.STMEventTypeMessage, Content: "Of course. What do you need help with?"},
	}

	// 2. Action
	result := analyzer.buildConversationText(events)

	// 3. Assert
	if !strings.Contains(result, "user: [message] Hello, can you help me?") {
		t.Error("Expected user message to be formatted correctly")
	}
	if !strings.Contains(result, "agent: [thought] User needs assistance.") {
		t.Error("Expected agent thought to be formatted correctly")
	}
	if !strings.Contains(result, "agent: [action] action: clarify_request") {
		t.Error("Expected agent action to be formatted correctly")
	}
	if !strings.Contains(result, "Turn 4:") {
		t.Error("Expected to find four turns in the output")
	}
	if !strings.Contains(result, "---") {
		t.Error("Expected '---' separator between turns")
	}
}

func TestTopicAnalyzer_AnalyzeTopics_LLMSuccess(t *testing.T) {
	// 1. Setup: Mock client returns a valid, nested JSON response from the LLM.
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		// The text field contains a stringified JSON array.
		llmTextContent := `[{"theme": "Unit Testing in Go", "keywords": ["unit", "mock", "test"], "content": "A detailed discussion about unit testing strategies in Go."}]`
		llmResponse := mockLLMChoiceResponse(llmTextContent)
		return mockJSONResponse(http.StatusOK, llmResponse)
	})

	analyzer := NewTopicAnalyzer(nil, nil)
	analyzer.HTTPClient = mockClient // Inject the mock client

	events := []models.CognitiveEvent{{Role: "user", Content: "Let's talk about testing."}}

	// 2. Action
	result, err := analyzer.AnalyzeTopics(context.Background(), events)

	// 3. Assert
	if err != nil {
		t.Fatalf("AnalyzeTopics failed unexpectedly: %v", err)
	}
	if result.Method != "llm" {
		t.Errorf("Expected method to be 'llm', got '%s'", result.Method)
	}
	if result.TotalTopics != 1 {
		t.Errorf("Expected TotalTopics to be 1, got %d", result.TotalTopics)
	}
	if result.MainTopic == nil {
		t.Fatal("MainTopic should not be nil")
	}
	if result.MainTopic.Theme != "Unit Testing in Go" {
		t.Errorf("Expected main topic theme to be 'Unit Testing in Go', got '%s'", result.MainTopic.Theme)
	}
	if len(result.MainTopic.Keywords) != 3 || result.MainTopic.Keywords[1] != "mock" {
		t.Errorf("Expected keywords to be parsed correctly, got %v", result.MainTopic.Keywords)
	}
}

func TestTopicAnalyzer_AnalyzeTopics_HeuristicFallback(t *testing.T) {
	// 1. Setup: Mock client returns a server error.
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		return mockJSONResponse(http.StatusInternalServerError, "LLM is down")
	})

	analyzer := NewTopicAnalyzer(nil, nil)
	analyzer.HTTPClient = mockClient // Inject the mock client

	events := []models.CognitiveEvent{{Role: "user", Content: "This call will fail."}}

	// 2. Action
	result, err := analyzer.AnalyzeTopics(context.Background(), events)

	// 3. Assert
	if err != nil {
		t.Fatalf("AnalyzeTopics should not return an error on fallback, but got: %v", err)
	}
	if result.Method != "heuristic_fallback" {
		t.Errorf("Expected method to be 'heuristic_fallback', got '%s'", result.Method)
	}
	// The current heuristic is a stub, so we expect 0 topics.
	if result.TotalTopics != 0 {
		t.Errorf("Expected TotalTopics to be 0 on heuristic fallback, got %d", result.TotalTopics)
	}
	if result.MainTopic != nil {
		t.Error("Expected MainTopic to be nil on heuristic fallback")
	}
}

// --- Mocking Helpers ---
// (Shared test helpers are now in test_helpers.go)
