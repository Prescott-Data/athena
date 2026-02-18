package memory

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// --- Test Setup ---

// setupContinuityTest initializes the analyzer and its dependencies with a mock client.
func setupContinuityTest(t *testing.T, mockClient *http.Client) (*ContinuityAnalyzer, ContinuityConfig) {
	t.Helper()

	// Mock STMStore and inject the test client.
	mockStore := NewSTMStore(nil, nil)
	mockStore.HTTPClient = mockClient

	// Mock ContinuityAnalyzer, injecting the mocked store and client.
	analyzer := NewContinuityAnalyzer(nil, mockStore)
	analyzer.HTTPClient = mockClient

	// Use default config but override thresholds for predictable tests.
	config := analyzer.GetDefaultConfig()
	config.ConfidenceThreshold = 0.8 // High similarity cutoff
	config.SemanticThreshold = 0.4   // Low similarity cutoff

	return analyzer, config
}

// --- Test Cases ---

func TestContinuityAnalyzer_calculateSemanticSimilarity(t *testing.T) {
	// 1. Setup: Mock client returns specific vectors for specific inputs.
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var respData interface{}
		if strings.Contains(string(bodyBytes), "summary 1") {
			respData = mockEmbeddingResponse([]float64{0.8, 0.6, 0.0})
		} else {
			respData = mockEmbeddingResponse([]float64{0.8, 0.6, 0.0}) // Identical vectors
		}
		return mockJSONResponse(http.StatusOK, respData)
	})

	analyzer, _ := setupContinuityTest(t, mockClient)
	chain1, chain2 := getTestChains("summary 1", "summary 2")

	// 2. Action: Calculate similarity.
	similarity, err := analyzer.calculateSemanticSimilarity(context.Background(), chain1, chain2)
	if err != nil {
		t.Fatalf("calculateSemanticSimilarity failed: %v", err)
	}

	// 3. Assert: Cosine similarity of identical normalized vectors should be ~1.0.
	if similarity < 0.999 {
		t.Errorf("Expected similarity to be ~1.0 for identical vectors, got %.5f", similarity)
	}
}

func TestContinuityAnalyzer_AnalyzeContinuity_HighSimilarity(t *testing.T) {
	// 1. Setup: Mock client returns highly similar embeddings.
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "embedding") {
			return nil, fmt.Errorf("unexpected request to non-embedding endpoint: %s", req.URL.Path)
		}
		var respData interface{}
		bodyBytes, _ := io.ReadAll(req.Body)
		if strings.Contains(string(bodyBytes), "first") {
			respData = mockEmbeddingResponse([]float64{1.0, 0.0, 0.0})
		} else {
			respData = mockEmbeddingResponse([]float64{0.9, 0.1, 0.0}) // High similarity
		}
		return mockJSONResponse(http.StatusOK, respData)
	})

	analyzer, config := setupContinuityTest(t, mockClient)
	chain1, chain2 := getTestChains("first summary", "second summary")

	// 2. Action: Analyze continuity.
	result, err := analyzer.AnalyzeContinuity(context.Background(), chain1, chain2, config)

	// 3. Assert: Should pass on semantics alone, without calling LLM.
	if err != nil {
		t.Fatalf("AnalyzeContinuity failed: %v", err)
	}
	if !result.IsContinuous {
		t.Error("Expected IsContinuous to be true for high similarity")
	}
	if result.AnalysisMethod != "semantic_only" {
		t.Errorf("Expected AnalysisMethod to be 'semantic_only', got '%s'", result.AnalysisMethod)
	}
}

func TestContinuityAnalyzer_AnalyzeContinuity_LLMFallback(t *testing.T) {
	// 1. Setup: Mock client returns gray-area embeddings, then a positive LLM response.
	llmCalled := false
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		// Embedding endpoint: return gray-area similarity.
		if strings.Contains(req.URL.Path, "embedding") {
			var respData interface{}
			bodyBytes, _ := io.ReadAll(req.Body)
			if strings.Contains(string(bodyBytes), "first") {
				respData = mockEmbeddingResponse([]float64{1.0, 0.0, 0.0})
			} else {
				// Similarity will be 0.6, which is between the 0.4 and 0.8 thresholds.
				respData = mockEmbeddingResponse([]float64{0.6, 0.8, 0.0})
			}
			return mockJSONResponse(http.StatusOK, respData)
		}
		// LLM endpoint: return a positive continuity response.
		// The path check here should match the LLM endpoint used in the code.
		if strings.Contains(req.URL.Path, "gpt-4o") || req.URL.Path == os.Getenv("LLM_BASE_URL") {
			llmCalled = true
			// Note: The analyzer expects a JSON string inside the 'text' field.
			llmContent := `{"score": 0.9, "reasoning": "Topics are closely related."}`
			llmResp := mockLLMChoiceResponse(llmContent)
			return mockJSONResponse(http.StatusOK, llmResp)
		}
		return nil, fmt.Errorf("unexpected request to %s", req.URL.Path)
	})

	analyzer, config := setupContinuityTest(t, mockClient)
	chain1, chain2 := getTestChains("first summary", "related summary")

	// 2. Action: Analyze continuity.
	result, err := analyzer.AnalyzeContinuity(context.Background(), chain1, chain2, config)

	// 3. Assert: Should use LLM and return a positive result.
	if err != nil {
		t.Fatalf("AnalyzeContinuity failed: %v", err)
	}
	if !llmCalled {
		t.Error("Expected LLM to be called for gray-area similarity, but it wasn't")
	}
	if !result.IsContinuous {
		t.Error("Expected IsContinuous to be true after LLM fallback")
	}
	if result.AnalysisMethod != "hybrid" {
		t.Errorf("Expected AnalysisMethod to be 'hybrid', got '%s'", result.AnalysisMethod)
	}
	if result.LLMScore == nil || *result.LLMScore != 0.9 {
		t.Errorf("Expected LLMScore to be 0.9, but got %v", result.LLMScore)
	}
}

// --- Mocking Helpers ---
// (Shared test helpers are now in test_helpers.go)
