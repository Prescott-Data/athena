package memory

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
)

// --- Mock HTTP Client ---

// MockRoundTripper implements http.RoundTripper to intercept HTTP calls for testing.
type MockRoundTripper struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

// RoundTrip executes the custom RoundTripFunc.
func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

// NewTestClient creates an *http.Client with a mock transport.
func NewTestClient(fn func(req *http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{
		Transport: &MockRoundTripper{
			RoundTripFunc: fn,
		},
	}
}

// --- Common Mock Response Helpers ---

// mockJSONResponse creates an HTTP response with JSON body.
func mockJSONResponse(statusCode int, data interface{}) (*http.Response, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// mockEmbeddingResponse creates a mock embedding API response.
func mockEmbeddingResponse(vector []float64) interface{} {
	return struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}{
		Data: []struct {
			Embedding []float64 `json:"embedding"`
		}{{Embedding: vector}},
	}
}

// mockLLMChoiceResponse mocks the specific nested structure for LLM text choices.
func mockLLMChoiceResponse(content string) interface{} {
	return struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}{
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{
			{
				Message: struct {
					Content string `json:"content"`
				}{
					Content: content,
				},
			},
		},
	}
}

// --- Common Test Data Helpers ---

// getTestChains creates two test cognitive chains for testing continuity.
func getTestChains(summary1, summary2 string) (*models.CognitiveChain, *models.CognitiveChain) {
	now := time.Now()
	return &models.CognitiveChain{
			ChainID:     "chain1",
			UserID:      "test-user",
			Summary:     summary1,
			LastEventAt: now.Add(-1 * time.Hour),
		}, &models.CognitiveChain{
			ChainID:   "chain2",
			UserID:    "test-user",
			Summary:   summary2,
			StartedAt: now,
		}
}
