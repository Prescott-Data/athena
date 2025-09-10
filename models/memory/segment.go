package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// HeatFactors represents the breakdown of heat score components
type HeatFactors struct {
	AccessFrequency  float64 `json:"accessFrequency" bson:"accessFrequency"`   // N_visit factor
	InteractionDepth float64 `json:"interactionDepth" bson:"interactionDepth"` // L_interaction factor
	RecencyScore     float64 `json:"recencyScore" bson:"recencyScore"`         // R_recency factor
	UserEngagement   float64 `json:"userEngagement" bson:"userEngagement"`     // User engagement factor
	TopicImportance  float64 `json:"topicImportance" bson:"topicImportance"`   // Topic importance factor
}

// Segment represents a grouped set of dialogue pages promoted from STM to MTM
type Segment struct {
	ID           primitive.ObjectID   `json:"id" bson:"_id,omitempty"`
	TenantID     string               `json:"tenantId" bson:"tenantId"`
	UserID       string               `json:"userId" bson:"userId"`
	AgentID      string               `json:"agentId" bson:"agentId"`
	SegmentID    string               `json:"segmentId" bson:"segmentId"`
	ChainID      string               `json:"chainId" bson:"chainId"`
	PageIDs      []primitive.ObjectID `json:"pageIds" bson:"pageIds"`
	TopicSummary string               `json:"topicSummary,omitempty" bson:"topicSummary,omitempty"`
	Scope        string               `json:"scope,omitempty" bson:"scope,omitempty"` // individual|team|org|global
	Status       string               `json:"status" bson:"status"`                   // "in_mtm" | "archived"

	// Enhanced heat scoring fields
	AccessCount     int64        `json:"accessCount,omitempty" bson:"accessCount,omitempty"`         // N_visit: Number of times segment was accessed
	InteractionSize int          `json:"interactionSize,omitempty" bson:"interactionSize,omitempty"` // L_interaction: Number of pages/complexity
	LastAccessTime  *time.Time   `json:"lastAccessTime,omitempty" bson:"lastAccessTime,omitempty"`   // For recency calculation
	HeatScore       float64      `json:"heatScore,omitempty" bson:"heatScore,omitempty"`             // Computed heat score
	HeatFactors     *HeatFactors `json:"heatFactors,omitempty" bson:"heatFactors,omitempty"`         // Heat score breakdown

	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" bson:"updatedAt"`
}
