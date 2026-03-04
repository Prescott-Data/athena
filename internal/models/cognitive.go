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
	ID           primitive.ObjectID     `json:"id" bson:"_id,omitempty"`
	TenantID     string                 `json:"tenantId" bson:"tenantId"`
	UserID       string                 `json:"userId" bson:"userId"`
	AgentID      string                 `json:"agentId" bson:"agentId"`
	ChainID      string                 `json:"chainId" bson:"chainId"`
	EventIndex   int                    `json:"eventIndex" bson:"eventIndex"`
	Role         string                 `json:"role"` // "user", "agent"
	Type         STMEventType           `json:"type"` // "message", "thought", "action", "observation"
	Content      string                 `json:"content"`
	BlobURI      string                 `json:"blobUri,omitempty" bson:"blobUri,omitempty"`
	BlobMimeType string                 `json:"blobMimeType,omitempty" bson:"blobMimeType,omitempty"`
	Status       string                 `json:"status"` // "in_stm", "archived"
	Metadata     map[string]interface{} `json:"metadata,omitempty" bson:"metadata,omitempty"`
	CreatedAt    time.Time              `json:"createdAt" bson:"createdAt"`
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
	Entities    []string           `json:"entities" bson:"entities,omitempty"`
	StartedAt   time.Time          `json:"startedAt" bson:"startedAt"`
	LastEventAt time.Time          `json:"lastEventAt" bson:"lastEventAt"`
	EventCount  int                `json:"eventCount" bson:"eventCount"`
	Status      string             `json:"status" bson:"status"` // "active", "archived"
	ArchivedAt  *time.Time         `json:"archivedAt,omitempty" bson:"archivedAt,omitempty"`
	// --- NEW FIELDS FOR HEAT SCORING ---
	IntrinsicImportance float64      `json:"intrinsicImportance" bson:"intrinsicImportance"` // Semantic score from LLM (0.0 - 1.0)
	RecallStrength      float64      `json:"recallStrength" bson:"recallStrength"`           // Starts at 1.0, grows with recall
	LastAccessedAt      *time.Time   `json:"lastAccessedAt" bson:"lastAccessedAt,omitempty"` // Timestamp of last recall
	DensityScore        float64      `json:"densityScore" bson:"densityScore"`               // Score for contextual density/system events
	HeatScore           float64      `json:"heatScore" bson:"heatScore,omitempty"`
	HeatFactors         *HeatFactors `json:"heatFactors" bson:"heatFactors,omitempty"`
	// --- END NEW FIELDS ---
	// Service-level metadata (e.g. origin_service, context_type) set at session creation.
	Metadata map[string]string `json:"metadata" bson:"metadata,omitempty"`
	// Transient — populated on vector search, not persisted to MongoDB.
	SimilarityScore float32 `json:"similarityScore,omitempty" bson:"-"`
}

// CognitiveChainCheckTask represents a lightweight task for background processing.
type CognitiveChainCheckTask struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"` // e.g., "cognitive_chain_check"
	TenantID   string    `json:"tenantId"`
	UserID     string    `json:"userId"`
	AgentID    string    `json:"agentId"`
	Timestamp  time.Time `json:"timestamp"`
	RetryCount int       `json:"retryCount,omitempty"`
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
