package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiProvider implements the Provider interface for Google Gemini
type GeminiProvider struct {
	BaseURL            string
	APIKey             string
	Model              string
	EmbeddingModelName string
	Client             *http.Client
}

func NewGeminiProvider(apiKey, model, embeddingModel string) *GeminiProvider {
	if model == "" {
		model = "gemini-1.5-pro"
	}
	if embeddingModel == "" {
		embeddingModel = "gemini-embedding-001"
	}
	// Google's API base URL is hardcoded or can be overridden if needed for enterprise
	baseURL := "https://generativelanguage.googleapis.com/v1beta/models"

	return &GeminiProvider{
		BaseURL:            baseURL,
		APIKey:             apiKey,
		Model:              model,
		EmbeddingModelName: embeddingModel,
		Client:             &http.Client{Timeout: 30 * time.Second},
	}
}

// EmbeddingModel returns the embedding model name in use.
func (p *GeminiProvider) EmbeddingModel() string {
	return p.EmbeddingModelName
}

// supportsThinkingBudget reports whether the model accepts
// generationConfig.thinkingConfig. Thinking-capable models spend the whole
// output budget on hidden reasoning for small max_tokens requests, so the
// pipeline disables thinking on them (see issue #20).
func supportsThinkingBudget(model string) bool {
	return strings.Contains(model, "2.5") || strings.Contains(model, "gemini-3")
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

	// Pipeline calls use small token budgets that thinking-capable models
	// would otherwise spend entirely on hidden reasoning.
	if supportsThinkingBudget(p.Model) {
		genCfg := requestBody["generationConfig"].(map[string]interface{})
		genCfg["thinkingConfig"] = map[string]interface{}{"thinkingBudget": 0}
	}

	if req.SystemPrompt != "" {
		requestBody["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{
				{"text": req.SystemPrompt},
			},
		}
	}

	if req.JSONSchema != nil {
		genConfig := requestBody["generationConfig"].(map[string]interface{})
		genConfig["responseMimeType"] = "application/json"

		schemaObj := req.JSONSchema
		if innerSchema, ok := req.JSONSchema["schema"].(map[string]interface{}); ok {
			schemaObj = innerSchema
		}

		genConfig["responseSchema"] = mapSchemaTypesForGemini(schemaObj)
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
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}
	if len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in response (finishReason=%s)", response.Candidates[0].FinishReason)
	}

	return response.Candidates[0].Content.Parts[0].Text, nil
}

func (p *GeminiProvider) CreateEmbedding(ctx context.Context, req EmbeddingRequest) ([]float64, error) {
	url := fmt.Sprintf("%s/%s:embedContent?key=%s", p.BaseURL, p.EmbeddingModelName, p.APIKey)

	requestBody := map[string]interface{}{
		"model": fmt.Sprintf("models/%s", p.EmbeddingModelName),
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

// mapSchemaTypesForGemini converts a JSON Schema into the shape Gemini's
// responseSchema accepts: type strings are uppercased, and JSON-Schema
// strictness keys the Schema proto rejects (additionalProperties, strict,
// $schema) are dropped recursively.
func mapSchemaTypesForGemini(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}

	result := make(map[string]interface{})
	for k, v := range schema {
		switch k {
		case "additionalProperties", "strict", "$schema":
			continue // not part of Gemini's Schema proto
		case "type":
			if typeStr, ok := v.(string); ok {
				result[k] = strings.ToUpper(typeStr)
			} else {
				result[k] = v
			}
		case "properties":
			if propsMap, ok := v.(map[string]interface{}); ok {
				newProps := make(map[string]interface{})
				for pk, pv := range propsMap {
					if subMap, mapped := pv.(map[string]interface{}); mapped {
						newProps[pk] = mapSchemaTypesForGemini(subMap)
					} else {
						newProps[pk] = pv
					}
				}
				result[k] = newProps
			}
		case "items":
			if itemsMap, ok := v.(map[string]interface{}); ok {
				result[k] = mapSchemaTypesForGemini(itemsMap)
			} else {
				result[k] = v
			}
		default:
			result[k] = v
		}
	}
	return result
}
