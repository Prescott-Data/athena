package janus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Triple represents a subject-predicate-object triple for knowledge graph storage
type Triple struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) *Client {
	if endpoint == "" {
		endpoint = "http://localhost:8182"
	}
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// gremlinRequest is the Gremlin Server HTTP request body
// See: Apache TinkerPop Gremlin Server REST API
// POST /gremlin {"gremlin":"g.V()...", "bindings":{}}
type gremlinRequest struct {
	Gremlin  string                 `json:"gremlin"`
	Bindings map[string]interface{} `json:"bindings,omitempty"`
}

type gremlinResponse struct {
	Result struct {
		Data interface{} `json:"data"`
	} `json:"result"`
	Status struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"status"`
}

func (c *Client) exec(ctx context.Context, gremlin string, bindings map[string]interface{}) error {
	reqBody := gremlinRequest{Gremlin: gremlin, Bindings: bindings}
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/gremlin", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gremlin exec failed: %w", err)
	}
	defer resp.Body.Close()
	
	var response gremlinResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode gremlin response: %w", err)
	}
	
	if response.Status.Code != 200 {
		return fmt.Errorf("gremlin error %d: %s", response.Status.Code, response.Status.Message)
	}
	
	return nil
}

// Health checks if JanusGraph is accessible
func (c *Client) Health(ctx context.Context) error {
	return c.exec(ctx, "g.V().count()", nil)
}

// CreateUserNode creates a user node in the knowledge graph
func (c *Client) CreateUserNode(ctx context.Context, userID string) error {
	gremlin := "g.V().has('user_id', userID).fold().coalesce(unfold(), addV('User').property('user_id', userID).property('created_at', created_at))"
	bindings := map[string]interface{}{
		"userID":     userID,
		"created_at": time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// CreateAgentNode creates an agent node in the knowledge graph
func (c *Client) CreateAgentNode(ctx context.Context, agentID string) error {
	gremlin := "g.V().has('agent_id', agentID).fold().coalesce(unfold(), addV('Agent').property('agent_id', agentID).property('created_at', created_at))"
	bindings := map[string]interface{}{
		"agentID":    agentID,
		"created_at": time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// AddUserPersonalityTriple adds a personality triple to the knowledge graph
func (c *Client) AddUserPersonalityTriple(ctx context.Context, userID, predicate, object string, confidence float64) error {
	// First ensure user node exists
	if err := c.CreateUserNode(ctx, userID); err != nil {
		return fmt.Errorf("failed to create user node: %w", err)
	}
	
	// Add the personality triple
	gremlin := `
		g.V().has('user_id', userID)
		.coalesce(
			outE(predicate).has('object', object),
			addE(predicate)
				.property('object', object)
				.property('confidence', confidence)
				.property('created_at', created_at)
				.property('updated_at', updated_at)
				.to(__.V().has('type', 'PersonalityAttribute').has('name', object).fold().coalesce(
					unfold(),
					addV('PersonalityAttribute').property('name', object).property('type', 'PersonalityAttribute')
				))
		)
	`
	bindings := map[string]interface{}{
		"userID":     userID,
		"predicate":  predicate,
		"object":     object,
		"confidence": confidence,
		"created_at": time.Now().Unix(),
		"updated_at": time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// AddUserFactTriple adds a user fact triple to the knowledge graph
func (c *Client) AddUserFactTriple(ctx context.Context, userID, predicate, object, category string, confidence float64) error {
	// First ensure user node exists
	if err := c.CreateUserNode(ctx, userID); err != nil {
		return fmt.Errorf("failed to create user node: %w", err)
	}
	
	// Add the fact triple
	gremlin := `
		g.V().has('user_id', userID)
		.coalesce(
			outE(predicate).has('object', object),
			addE(predicate)
				.property('object', object)
				.property('category', category)
				.property('confidence', confidence)
				.property('created_at', created_at)
				.property('updated_at', updated_at)
				.to(__.V().has('type', 'Fact').has('name', object).fold().coalesce(
					unfold(),
					addV('Fact').property('name', object).property('type', 'Fact').property('category', category)
				))
		)
	`
	bindings := map[string]interface{}{
		"userID":     userID,
		"predicate":  predicate,
		"object":     object,
		"category":   category,
		"confidence": confidence,
		"created_at": time.Now().Unix(),
		"updated_at": time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// AddAgentCapabilityTriple adds an agent capability triple to the knowledge graph
func (c *Client) AddAgentCapabilityTriple(ctx context.Context, agentID, capability, context_info string) error {
	// First ensure agent node exists
	if err := c.CreateAgentNode(ctx, agentID); err != nil {
		return fmt.Errorf("failed to create agent node: %w", err)
	}
	
	// Add the capability triple
	gremlin := `
		g.V().has('agent_id', agentID)
		.coalesce(
			outE('CAN_DO').has('capability', capability),
			addE('CAN_DO')
				.property('capability', capability)
				.property('context', context_info)
				.property('demonstrated_at', demonstrated_at)
				.to(__.V().has('type', 'Capability').has('name', capability).fold().coalesce(
					unfold(),
					addV('Capability').property('name', capability).property('type', 'Capability')
				))
		)
	`
	bindings := map[string]interface{}{
		"agentID":        agentID,
		"capability":     capability,
		"context_info":   context_info,
		"demonstrated_at": time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// GetUserFacts retrieves user facts from the knowledge graph
func (c *Client) GetUserFacts(ctx context.Context, userID string) ([]map[string]interface{}, error) {
	reqBody := gremlinRequest{
		Gremlin: "g.V().has('user_id', userID).outE().project('predicate', 'object', 'confidence').by(label()).by('object').by('confidence')",
		Bindings: map[string]interface{}{
			"userID": userID,
		},
	}
	
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/gremlin", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gremlin query failed: %w", err)
	}
	defer resp.Body.Close()
	
	var response gremlinResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode gremlin response: %w", err)
	}
	
	if response.Status.Code != 200 {
		return nil, fmt.Errorf("gremlin error %d: %s", response.Status.Code, response.Status.Message)
	}
	
	// Convert response data to slice of maps
	facts, ok := response.Result.Data.([]interface{})
	if !ok {
		return []map[string]interface{}{}, nil
	}
	
	result := make([]map[string]interface{}, len(facts))
	for i, fact := range facts {
		if factMap, ok := fact.(map[string]interface{}); ok {
			result[i] = factMap
		}
	}
	
	return result, nil
}

// UpdateTripleConfidence updates the confidence score of an existing triple
func (c *Client) UpdateTripleConfidence(ctx context.Context, userID, predicate, object string, newConfidence float64) error {
	gremlin := `
		g.V().has('user_id', userID)
		.outE(predicate).has('object', object)
		.property('confidence', newConfidence)
		.property('updated_at', updated_at)
	`
	bindings := map[string]interface{}{
		"userID":        userID,
		"predicate":     predicate,
		"object":        object,
		"newConfidence": newConfidence,
		"updated_at":    time.Now().Unix(),
	}
	return c.exec(ctx, gremlin, bindings)
}

// DeleteTriple removes a triple from the knowledge graph
func (c *Client) DeleteTriple(ctx context.Context, userID, predicate, object string) error {
	gremlin := `
		g.V().has('user_id', userID)
		.outE(predicate).has('object', object)
		.drop()
	`
	bindings := map[string]interface{}{
		"userID":    userID,
		"predicate": predicate,
		"object":    object,
	}
	return c.exec(ctx, gremlin, bindings)
}
