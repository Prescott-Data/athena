package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// DialoguePage represents a single conversational turn stored in the STM Store
type DialoguePage struct {
	ID          primitive.ObjectID     `json:"id" bson:"_id,omitempty"`
	UserID      string                 `json:"userId" bson:"userId"`
	ChainID     string                 `json:"chainId" bson:"chainId"`
	TurnIndex   int                    `json:"turnIndex" bson:"turnIndex"`
	UserMessage string                 `json:"userMessage" bson:"userMessage"`
	AgentResponse string               `json:"agentResponse" bson:"agentResponse"`
	Status      string                 `json:"status" bson:"status"` // "in_stm", "archived"
	Metadata    map[string]interface{} `json:"metadata,omitempty" bson:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"createdAt" bson:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt" bson:"updatedAt"`
}

// DialogueChain represents metadata about a conversational topic chain
type DialogueChain struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	ChainID     string             `json:"chainId" bson:"chainId"`
	UserID      string             `json:"userId" bson:"userId"`
	Topic       string             `json:"topic" bson:"topic"`
	Summary     string             `json:"summary" bson:"summary"`
	StartedAt   time.Time          `json:"startedAt" bson:"startedAt"`
	LastTurnAt  time.Time          `json:"lastTurnAt" bson:"lastTurnAt"`
	TurnCount   int                `json:"turnCount" bson:"turnCount"`
	Status      string             `json:"status" bson:"status"` // "active", "archived"
}

// MemoryProcessingTask represents a task for background processing
type MemoryProcessingTask struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"` // "memory_formation"
	UserID        string                 `json:"userId"`
	UserMessage   string                 `json:"userMessage"`
	AgentResponse string                 `json:"agentResponse"`
	Timestamp     time.Time              `json:"timestamp"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt     time.Time              `json:"createdAt"`
}

// TopicContinuityRequest represents the request for LLM topic analysis
type TopicContinuityRequest struct {
	PreviousTurn ConversationTurnText `json:"previousTurn"`
	NewTurn      ConversationTurnText `json:"newTurn"`
}

// ConversationTurnText represents a turn for topic analysis
type ConversationTurnText struct {
	UserMessage   string `json:"userMessage"`
	AgentResponse string `json:"agentResponse"`
}

// TopicContinuityResponse represents the LLM's topic analysis response
type TopicContinuityResponse struct {
	IsContinuous bool   `json:"is_continuous"`
	Reasoning    string `json:"reasoning,omitempty"`
}

// EmbeddingData represents vector embedding information
type EmbeddingData struct {
	PageID     string    `json:"pageId"`
	Vector     []float64 `json:"vector"`
	Dimensions int       `json:"dimensions"`
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"createdAt"`
}