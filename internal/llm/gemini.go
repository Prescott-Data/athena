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

// GeminiProvider implements the Provider interface for Google Gemini
type GeminiProvider struct {
	BaseURL        string
	APIKey         string
	Model          string
	EmbeddingModel string
	Client         *http.Client
}

func NewGeminiProvider(apiKey, model, embeddingModel string) *GeminiProvider {
	if model == "" {
		model = "gemini-1.5-pro"
	}
	if embeddingModel == "" {
		embeddingModel = "text-embedding-004"
	}
	// Google's API base URL is hardcoded or can be overridden if needed for enterprise
	baseURL := "https://generativelanguage.googleapis.com/v1beta/models"
	
	return &GeminiProvider{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		Model:          model,
		EmbeddingModel: embeddingModel,
		Client:         &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *GeminiProvider) GenerateCompletion(ctx context.Context, req CompletionRequest) (string, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", p.BaseURL, p.Model, p.APIKey)

	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": req.Prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": req.MaxTokens,
			"temperature":     req.Temperature,
			"stopSequences":   req.Stop,
		},
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to call Gemini API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	return response.Candidates[0].Content.Parts[0].Text, nil
}

func (p *GeminiProvider) CreateEmbedding(ctx context.Context, req EmbeddingRequest) ([]float64, error) {
	url := fmt.Sprintf("%s/%s:embedContent?key=%s", p.BaseURL, p.EmbeddingModel, p.APIKey)

	requestBody := map[string]interface{}{
		"model": fmt.Sprintf("models/%s", p.EmbeddingModel),
		"content": map[string]interface{}{
			"parts": []map[string]string{
				{"text": req.Input},
			},
		},
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	if len(response.Embedding.Values) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return response.Embedding.Values, nil
}
