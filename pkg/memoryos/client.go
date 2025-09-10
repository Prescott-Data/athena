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
	baseURL    string
	httpClient *http.Client
	apiKey     string
	jwtToken   string
}

// ClientConfig holds configuration for the Memory OS client
type ClientConfig struct {
	BaseURL    string
	APIKey     string
	JWTToken   string
	Timeout    time.Duration
	EnableTLS  bool
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
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// API Request/Response Models

// StoreInteractionRequest represents a request to store an interaction
type StoreInteractionRequest struct {
	UserMessage   string            `json:"user_message"`
	AgentResponse string            `json:"agent_response"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Timestamp     time.Time         `json:"timestamp"`
}

// StoreInteractionResponse represents the response from storing an interaction
type StoreInteractionResponse struct {
	Success       bool   `json:"success"`
	InteractionID string `json:"interaction_id"`
}

// ConversationContext represents conversation context
type ConversationContext struct {
	SessionID     string              `json:"session_id"`
	RecentTurns   []ConversationTurn  `json:"recent_turns"`
	RelevantPages []DialoguePage      `json:"relevant_pages"`
	Segments      []Segment           `json:"segments,omitempty"`
	UserPersona   *UserPersona        `json:"user_persona,omitempty"`
}

// ConversationTurn represents a single conversation turn
type ConversationTurn struct {
	UserMessage   string    `json:"user_message"`
	AgentResponse string    `json:"agent_response"`
	Timestamp     time.Time `json:"timestamp"`
}

// DialoguePage represents a dialogue page
type DialoguePage struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	UserMessage   string            `json:"user_message"`
	AgentResponse string            `json:"agent_response"`
	Timestamp     time.Time         `json:"timestamp"`
	Embedding     []float64         `json:"embedding,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Segment represents a memory segment
type Segment struct {
	ID           string       `json:"id"`
	SessionID    string       `json:"session_id"`
	Content      string       `json:"content"`
	Topics       []string     `json:"topics"`
	HeatFactors  *HeatFactors `json:"heat_factors"`
	QualityScore float64      `json:"quality_score"`
	CreatedAt    time.Time    `json:"created_at"`
}

// HeatFactors represents heat scoring factors
type HeatFactors struct {
	AccessFrequency  float64 `json:"access_frequency"`
	InteractionDepth float64 `json:"interaction_depth"`
	RecencyScore     float64 `json:"recency_score"`
	UserEngagement   float64 `json:"user_engagement"`
	TopicImportance  float64 `json:"topic_importance"`
}

// UserPersona represents user personality information
type UserPersona struct {
	UserID            string            `json:"user_id"`
	PersonalityTraits map[string]float64 `json:"personality_traits"`
	Interests         []string          `json:"interests"`
	Preferences       map[string]string `json:"preferences"`
}

// CreateSessionRequest represents a request to create a session
type CreateSessionRequest struct {
	UserID   string            `json:"user_id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CreateSessionResponse represents the response from creating a session
type CreateSessionResponse struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchMemoryRequest represents a memory search request
type SearchMemoryRequest struct {
	Query               string  `json:"query"`
	Limit               int     `json:"limit,omitempty"`
	SimilarityThreshold float64 `json:"similarity_threshold,omitempty"`
}

// SearchResult represents a search result
type SearchResult struct {
	Content         string    `json:"content"`
	SimilarityScore float64   `json:"similarity_score"`
	SourceType      string    `json:"source_type"`
	SourceID        string    `json:"source_id"`
	Timestamp       time.Time `json:"timestamp"`
}

// Client Methods

// CreateSession creates a new memory session
func (c *Client) CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error) {
	var resp CreateSessionResponse
	err := c.doRequest(ctx, "POST", "/api/v1/sessions", req, &resp)
	return &resp, err
}

// StoreInteraction stores an interaction in memory
func (c *Client) StoreInteraction(ctx context.Context, sessionID string, req *StoreInteractionRequest) (*StoreInteractionResponse, error) {
	var resp StoreInteractionResponse
	path := fmt.Sprintf("/api/v1/sessions/%s/interactions", sessionID)
	err := c.doRequest(ctx, "POST", path, req, &resp)
	return &resp, err
}

// GetContext retrieves conversation context for a session
func (c *Client) GetContext(ctx context.Context, sessionID string, limit int) (*ConversationContext, error) {
	var resp ConversationContext
	path := fmt.Sprintf("/api/v1/sessions/%s/context", sessionID)
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	err := c.doRequest(ctx, "GET", path, nil, &resp)
	return &resp, err
}

// SearchMemory performs semantic search in memory
func (c *Client) SearchMemory(ctx context.Context, sessionID string, req *SearchMemoryRequest) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	path := fmt.Sprintf("/api/v1/sessions/%s/context/search", sessionID)
	err := c.doRequest(ctx, "POST", path, req, &resp)
	return resp.Results, err
}

// GetSegments retrieves memory segments for a session
func (c *Client) GetSegments(ctx context.Context, sessionID string, limit int) ([]Segment, error) {
	var resp struct {
		Segments []Segment `json:"segments"`
	}
	path := fmt.Sprintf("/api/v1/sessions/%s/segments", sessionID)
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	err := c.doRequest(ctx, "GET", path, nil, &resp)
	return resp.Segments, err
}

// HealthCheck checks the health of the Memory OS service
func (c *Client) HealthCheck(ctx context.Context) (map[string]interface{}, error) {
	var resp map[string]interface{}
	err := c.doRequest(ctx, "GET", "/health", nil, &resp)
	return resp, err
}

// doRequest performs an HTTP request to the Memory OS API
func (c *Client) doRequest(ctx context.Context, method, path string, reqBody, respBody interface{}) error {
	var body io.Reader
	
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		body = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	
	if c.jwtToken != "" {
		req.Header.Set("X-JWT-Token", c.jwtToken)
	}

	// Perform request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode >= 400 {
		var errorResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		
		if err := json.Unmarshal(respData, &errorResp); err == nil {
			return fmt.Errorf("API error (%d): %s - %s", resp.StatusCode, errorResp.Error, errorResp.Message)
		}
		
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respData))
	}

	// Parse response body
	if respBody != nil {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("failed to unmarshal response body: %w", err)
		}
	}

	return nil
}

// SetJWTToken updates the JWT token for authentication
func (c *Client) SetJWTToken(token string) {
	c.jwtToken = token
}

// SetAPIKey updates the API key for authentication
func (c *Client) SetAPIKey(apiKey string) {
	c.apiKey = apiKey
}
