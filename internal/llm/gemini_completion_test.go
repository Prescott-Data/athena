package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Vertex AI answers 400 "Please use a valid role: user, model." when a content
// has no role, so every completion must send one.
func TestGenerateCompletion_SendsUserRole(t *testing.T) {
	var got struct {
		Contents []struct {
			Role string `json:"role"`
		} `json:"contents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	p := NewGeminiProvider("test-key", "gemini-3-flash-preview", "")
	p.BaseURL = srv.URL
	if _, err := p.GenerateCompletion(context.Background(), CompletionRequest{Prompt: "Reply with OK.", MaxTokens: 5}); err != nil {
		t.Fatalf("GenerateCompletion: %v", err)
	}
	if len(got.Contents) != 1 || got.Contents[0].Role != "user" {
		t.Fatalf("contents = %+v, want one content with role \"user\"", got.Contents)
	}
}
