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
	BaseURL        string
	APIKey         string
	EmbeddingURL   string
	EmbeddingModel string
	Client         *http.Client
}

func NewAzureProvider(baseURL, apiKey, embeddingURL, embeddingModel string) *AzureProvider {
	return &AzureProvider{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		EmbeddingURL:   embeddingURL,
		EmbeddingModel: embeddingModel,
		Client:         &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *AzureProvider) GenerateCompletion(ctx context.Context, req CompletionRequest) (string, error) {
	requestBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}
	if len(req.Stop) > 0 {
		requestBody["stop"] = req.Stop
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.APIKey)

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to call Azure OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Azure OpenAI API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return response.Choices[0].Message.Content, nil
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
