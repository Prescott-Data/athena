package memory

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"bitbucket.org/dromos/athena-memos/internal/models"
)

// --- Test Setup ---

func setupProcessorTest(t *testing.T, mockClient *http.Client) *ParallelProcessor {
	t.Helper()
	// The stmStore is needed as a dependency for the processor's analysis tasks.
	mockStore := NewSTMStore(nil, nil, nil)
	if mockClient != nil {
		// Inject the mock client into the store, which is used by the topic analyzer.
		mockStore.HTTPClient = mockClient
		mockStore.topicAnalyzer.HTTPClient = mockClient
	}
	// Concurrency of 2 for testing.
	processor := NewParallelProcessor(2, mockStore)
	return processor
}

// --- Test Cases ---

func TestParallelProcessor_ProcessTasks_Generic(t *testing.T) {
	// 1. Setup: Create a processor (no store needed for generic tasks) and some simple tasks.
	processor := NewParallelProcessor(2, nil)
	taskCount := 4
	var tasksProcessed int32

	tasks := make([]*ProcessingTask, taskCount)
	for i := 0; i < taskCount; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		tasks[i] = &ProcessingTask{
			ID: taskID,
			Execute: func() (interface{}, error) {
				time.Sleep(10 * time.Millisecond)
				atomic.AddInt32(&tasksProcessed, 1)
				return "done", nil
			},
		}
	}

	// 2. Action
	start := time.Now()
	results, err := processor.ProcessTasks(context.Background(), tasks)
	duration := time.Since(start)

	// 3. Assert
	if err != nil {
		t.Fatalf("ProcessTasks returned an error: %v", err)
	}
	if len(results) != taskCount {
		t.Fatalf("Expected %d results, got %d", taskCount, len(results))
	}
	if atomic.LoadInt32(&tasksProcessed) != int32(taskCount) {
		t.Errorf("Expected %d tasks to be processed, but counter was %d", taskCount, tasksProcessed)
	}

	// With 2 workers, 4 tasks of 10ms should take ~20ms, not 40ms.
	if duration >= 35*time.Millisecond {
		t.Errorf("Expected tasks to run in parallel, but duration was %v", duration)
	}

	for _, res := range results {
		if res.Error != nil {
			t.Errorf("Task %s failed: %v", res.TaskID, res.Error)
		}
		if res.Result.(string) != "done" {
			t.Errorf("Task %s result was incorrect", res.TaskID)
		}
	}
}

func TestParallelProcessor_analyzeChain_FullAnalysis(t *testing.T) {
	// 1. Setup: Mock the LLM response for the topic analyzer.
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		llmTextContent := `[{"theme": "Go Keywords", "keywords": ["go", "testing", "mock"], "content": "Summary about Go testing."}]`
		llmResponse := mockLLMChoiceResponse(llmTextContent)
		return mockJSONResponse(http.StatusOK, llmResponse)
	})
	processor := setupProcessorTest(t, mockClient)

	task := &ChainAnalysisTask{
		TaskType: "full",
		Chain:    &models.CognitiveChain{ChainID: "chain-1", Summary: "A chain about Go testing."},
		Events:   []models.CognitiveEvent{{Content: "Let's talk about testing in Go."}},
	}

	// 2. Action
	result := processor.analyzeChain(context.Background(), task)

	// 3. Assert
	if result.Error != nil {
		t.Fatalf("analyzeChain failed: %v", result.Error)
	}
	// Quality score comes from the dummy logic in QualityValidator, just check it's calculated.
	if result.QualityScore <= 0 {
		t.Errorf("Expected QualityScore to be calculated and positive, got %.2f", result.QualityScore)
	}
	// Keywords come from the mocked LLM response.
	if len(result.Keywords) != 3 || result.Keywords[0] != "go" {
		t.Errorf("Expected keywords to be ['go', 'testing', 'mock'], got %v", result.Keywords)
	}
	if result.Summary != "Summary about Go testing." {
		t.Errorf("Expected summary to be 'Summary about Go testing.', got '%s'", result.Summary)
	}
}

func TestParallelProcessor_analyzeChain_SummaryOnly_Mocked(t *testing.T) {
	// 1. Setup: Mock the LLM to return a specific summary.
	mockSummary := "This is a mock summary from the LLM."
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		llmTextContent := fmt.Sprintf(`[{"theme": "Mock Summary", "keywords": [], "content": "%s"}]`, mockSummary)
		llmResponse := mockLLMChoiceResponse(llmTextContent)
		return mockJSONResponse(http.StatusOK, llmResponse)
	})
	processor := setupProcessorTest(t, mockClient)

	task := &ChainAnalysisTask{
		TaskType: "summary",
		Chain:    &models.CognitiveChain{ChainID: "chain-summary"},
		Events:   []models.CognitiveEvent{{Content: "This content will be summarized."}},
	}

	// 2. Action
	result := processor.analyzeChain(context.Background(), task)

	// 3. Assert
	if result.Error != nil {
		t.Fatalf("analyzeChain failed for summary task: %v", result.Error)
	}
	if result.Summary != mockSummary {
		t.Errorf("Expected summary to be '%s', but got '%s'", mockSummary, result.Summary)
	}
}

// --- Mocking Helpers ---
// (Shared test helpers are now in test_helpers.go)
