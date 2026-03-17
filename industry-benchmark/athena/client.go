// Package athena provides a minimal REST client for the Athena Memory-OS API.
// Used exclusively by the industry-benchmark experiments.
package athena

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client talks to the Athena Memory-OS REST API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ---------- Request / Response Types ----------

type CreateSessionRequest struct {
	TenantID string            `json:"tenant_id"`
	UserID   string            `json:"user_id"`
	AgentID  string            `json:"agent_id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type StoreInteractionRequest struct {
	UserMessage   string            `json:"userMessage"`
	AgentResponse string            `json:"agentResponse"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type StoreInteractionResponse struct {
	Success       bool   `json:"success"`
	InteractionID string `json:"interactionId,omitempty"`
}

type StoreEventRequest struct {
	Role     string            `json:"role"`
	Type     string            `json:"type"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type StoreEventResponse struct {
	Success bool   `json:"success"`
	EventID string `json:"eventId,omitempty"`
}

type GetContextRequest struct {
	Limit int    `json:"limit,omitempty"`
	Query string `json:"query,omitempty"`
}

type DialoguePage struct {
	ID      string `json:"id"`
	Topic   string `json:"topic"`
	Summary string `json:"summary"`
}

type STMEvent struct {
	Role    string `json:"role"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type GetContextResponse struct {
	STMEvents     []STMEvent     `json:"stmEvents,omitempty"`
	RelevantPages []DialoguePage `json:"relevantPages,omitempty"`
}

type SearchMemoryRequest struct {
	Query               string  `json:"query"`
	Limit               int     `json:"limit,omitempty"`
	SimilarityThreshold float64 `json:"similarityThreshold,omitempty"`
}

type SearchResult struct {
	Content         string  `json:"content"`
	SimilarityScore float64 `json:"similarityScore,omitempty"`
	SourceType      string  `json:"sourceType,omitempty"`
}

type SearchMemoryResponse struct {
	Results []SearchResult `json:"results,omitempty"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// ---------- API Methods ----------

func (c *Client) HealthCheck() (*HealthResponse, error) {
	resp, err := c.do("GET", "/health", nil)
	if err != nil {
		return nil, err
	}
	var r HealthResponse
	return &r, json.Unmarshal(resp, &r)
}

func (c *Client) CreateSession(req CreateSessionRequest) (*CreateSessionResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", "/api/v1/sessions", body)
	if err != nil {
		return nil, err
	}
	var r CreateSessionResponse
	return &r, json.Unmarshal(resp, &r)
}

func (c *Client) StoreInteraction(sessionID string, req StoreInteractionRequest) (*StoreInteractionResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", fmt.Sprintf("/api/v1/sessions/%s/interactions", sessionID), body)
	if err != nil {
		return nil, err
	}
	var r StoreInteractionResponse
	return &r, json.Unmarshal(resp, &r)
}

func (c *Client) StoreEvent(sessionID string, req StoreEventRequest) (*StoreEventResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", fmt.Sprintf("/api/v1/sessions/%s/events", sessionID), body)
	if err != nil {
		return nil, err
	}
	var r StoreEventResponse
	return &r, json.Unmarshal(resp, &r)
}

func (c *Client) GetContext(sessionID string, req GetContextRequest) (*GetContextResponse, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s/context?limit=%d", sessionID, req.Limit)
	if req.Query != "" {
		path += "&query=" + url.QueryEscape(req.Query)
	}
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var r GetContextResponse
	return &r, json.Unmarshal(resp, &r)
}

func (c *Client) SearchMemory(sessionID string, req SearchMemoryRequest) (*SearchMemoryResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", fmt.Sprintf("/api/v1/sessions/%s/context/search", sessionID), body)
	if err != nil {
		return nil, err
	}
	var r SearchMemoryResponse
	return &r, json.Unmarshal(resp, &r)
}

// ---------- HTTP Helper ----------

func (c *Client) do(method, path string, body []byte) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewBuffer(body)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
