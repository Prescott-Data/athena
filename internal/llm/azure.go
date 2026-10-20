package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AzureProvider implements the Provider interface for Azure OpenAI
type AzureProvider struct {
	BaseURL            string
	APIKey             string
	EmbeddingURL       string
	EmbeddingModelName string
	Client             *http.Client
	compat             *chatCompat
}

func NewAzureProvider(baseURL, apiKey, embeddingURL, embeddingModel string) *AzureProvider {
	if embeddingModel == "" {
		embeddingModel = "text-embedding-ada-002"
	}
	return &AzureProvider{
		BaseURL:            baseURL,
		APIKey:             apiKey,
		EmbeddingURL:       embeddingURL,
		EmbeddingModelName: embeddingModel,
		Client:             &http.Client{Timeout: 30 * time.Second},
		compat:             newChatCompat(),
	}
}

// EmbeddingModel returns the embedding model name in use.
func (p *AzureProvider) EmbeddingModel() string {
	return p.EmbeddingModelName
}

func (p *AzureProvider) GenerateCompletion(ctx context.Context, req CompletionRequest) (string, error) {
	return postChatCompletion(ctx, p.Client, p.BaseURL, "Azure OpenAI", p.compat, req, nil, func(r *http.Request) {
		r.Header.Set("api-key", p.APIKey)
	})
}

func (p *AzureProvider) CreateEmbedding(ctx context.Context, req EmbeddingRequest) ([]float64, error) {
	requestBody := map[string]interface{}{
		"input": req.Input,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.EmbeddingURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.APIKey)

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call embedding service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return response.Data[0].Embedding, nil
}
