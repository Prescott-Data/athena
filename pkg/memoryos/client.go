package memoryos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the Memory OS client
type Client struct {
	baseURL  string
	apiKey   string
	jwtToken string
	http     *http.Client
}

// ClientConfig holds the configuration for the Memory OS client
type ClientConfig struct {
	BaseURL  string
	APIKey   string
	JWTToken string
	Timeout  time.Duration
}

// NewClient creates a new Memory OS client
func NewClient(config ClientConfig) *Client {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	return &Client{
		baseURL:  config.BaseURL,
		apiKey:   config.APIKey,
		jwtToken: config.JWTToken,
		http: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// API Models (simplified for client usage)

type CreateSessionRequest struct {
	UserID   string            `json:"user_id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CreateSessionResponse struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

type StoreInteractionRequest struct {
	UserMessage   string    `json:"user_message"`
	AgentResponse string    `json:"agent_response"`
	Timestamp     time.Time `json:"timestamp"`
}

type StoreInteractionResponse struct {
	Success       bool   `json:"success"`
	InteractionID string `json:"interaction_id"`
}

type GetContextResponse struct {
	RecentEvents []STMEvent `json:"recent_turns"`
}

// STMEvent represents a single event in the short-term memory.
type STMEvent struct {
	UserMessage   string    `json:"user_message"`
	AgentResponse string    `json:"agent_response"`
	Timestamp     time.Time `json:"timestamp"`
}

// CreateSession creates a new memory session
func (c *Client) CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/sessions", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var sessionResp CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &sessionResp, nil
}

// StoreInteraction stores a new interaction in a session
func (c *Client) StoreInteraction(ctx context.Context, sessionID string, req *StoreInteractionRequest) (*StoreInteractionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sessions/%s/interactions", c.baseURL, sessionID)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var interactionResp StoreInteractionResponse
	if err := json.NewDecoder(resp.Body).Decode(&interactionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &interactionResp, nil
}

// GetContext retrieves the recent context for a session
func (c *Client) GetContext(ctx context.Context, sessionID string, limit int) (*GetContextResponse, error) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s/context?limit=%d", c.baseURL, sessionID, limit)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var contextResp GetContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&contextResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &contextResp, nil
}

// setAuthHeaders adds the appropriate authentication headers to the request
func (c *Client) setAuthHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.jwtToken != "" {
		req.Header.Set("X-JWT-Token", c.jwtToken)
	}
}

// handleError parses a non-200 response and returns a formatted error
func (c *Client) handleError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
}
