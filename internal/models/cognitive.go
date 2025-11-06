package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// STMEventType defines the type of event stored in the STM.
type STMEventType string

const (
	// STMEventTypeMessage represents a message from a user or agent.
	STMEventTypeMessage STMEventType = "message"
	// STMEventTypeThought represents an internal reasoning step of the agent.
	STMEventTypeThought STMEventType = "thought"
	// STMEventTypeAction represents a concrete action the agent is taking (e.g., tool call).
	STMEventTypeAction STMEventType = "action"
	// STMEventTypeObservation represents the result or output of an action.
	STMEventTypeObservation STMEventType = "observation"
)

// CognitiveEvent represents a single event in a cognitive chain, stored in MongoDB.
type CognitiveEvent struct {
	ID         primitive.ObjectID     `json:"id" bson:"_id,omitempty"`
	TenantID   string                 `json:"tenantId" bson:"tenantId"`
	UserID     string                 `json:"userId" bson:"userId"`
	AgentID    string                 `json:"agentId" bson:"agentId"`
	ChainID    string                 `json:"chainId" bson:"chainId"`
	EventIndex int                    `json:"eventIndex" bson:"eventIndex"`
	Role       string                 `json:"role"`      // "user", "agent"
	Type       STMEventType           `json:"type"`      // "message", "thought", "action", "observation"
	Content    string                 `json:"content"`
	Status     string                 `json:"status"` // "in_stm", "archived"
	Metadata   map[string]interface{} `json:"metadata,omitempty" bson:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"createdAt" bson:"createdAt"`
}

// CognitiveChain represents metadata about a cognitive chain.
type CognitiveChain struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	TenantID    string             `json:"tenantId" bson:"tenantId"`
	UserID      string             `json:"userId" bson:"userId"`
	AgentID     string             `json:"agentId" bson:"agentId"`
	ChainID     string             `json:"chainId" bson:"chainId"`
	Topic       string             `json:"topic" bson:"topic"`
	Summary     string             `json:"summary" bson:"summary"`
	StartedAt   time.Time          `json:"startedAt" bson:"startedAt"`
	LastEventAt time.Time          `json:"lastEventAt" bson:"lastEventAt"`
	EventCount  int                `json:"eventCount" bson:"eventCount"`
	Status      string             `json:"status" bson:"status"` // "active", "archived"
}

// CognitiveChainCheckTask represents a lightweight task for background processing.
type CognitiveChainCheckTask struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // e.g., "cognitive_chain_check"
	TenantID  string    `json:"tenantId"`
	UserID    string    `json:"userId"`
	AgentID   string    `json:"agentId"`
	Timestamp time.Time `json:"timestamp"`
}

// EmbeddingData represents vector embedding information.
type EmbeddingData struct {
	TenantID    string    `json:"tenantId"`
	UserID      string    `json:"userId"`
	AgentID     string    `json:"agentId"`
	ReferenceID string    `json:"referenceId"` // Reference to CognitiveChain.ChainID or other
	Vector      []float64 `json:"vector"`
	Dimensions  int       `json:"dimensions"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"createdAt"`
}

// DialoguePage is a temporary placeholder for the old data model.
// It is used to allow legacy MTM/LPM code to compile after the STM refactor.
// TODO: Refactor all modules using DialoguePage to use CognitiveEvent instead.
type DialoguePage struct {
	ID            primitive.ObjectID     `bson:"_id,omitempty"`
	TenantID      string                 `bson:"tenantId"`
	UserID        string                 `bson:"userId"`
	AgentID       string                 `bson:"agentId"`
	ChainID       string                 `bson:"chainId"`
	UserMessage   string                 `bson:"userMessage"`
	AgentResponse string                 `bson:"agentResponse"`
	TurnIndex     int                    `bson:"turnIndex"`
	Status        string                 `bson:"status"`
	Metadata      map[string]interface{} `bson:"metadata,omitempty"`
	CreatedAt     time.Time              `bson:"createdAt"`
	UpdatedAt     time.Time              `bson:"updatedAt"`
}