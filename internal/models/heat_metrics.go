package models

import "time"

// HeatFactors represents the breakdown of heat score components
type HeatFactors struct {
	AccessFrequency  float64 `json:"accessFrequency" bson:"accessFrequency"`   // N_visit factor
	InteractionDepth float64 `json:"interactionDepth" bson:"interactionDepth"` // L_interaction factor
	RecencyScore     float64 `json:"recencyScore" bson:"recencyScore"`         // R_recency factor
	UserEngagement   float64 `json:"userEngagement" bson:"userEngagement"`     // User engagement factor
	TopicImportance  float64 `json:"topicImportance" bson:"topicImportance"`   // Topic importance factor
	CognitiveDepth   float64 `json:"cognitiveDepth" bson:"cognitiveDepth"`     // Cognitive depth factor (thoughts/actions)
}

// HeatMetrics represents aggregated heat metrics for a session or user
type HeatMetrics struct {
	OverallHeat       float64      `json:"overallHeat" bson:"overallHeat"`
	Breakdown         *HeatFactors `json:"breakdown" bson:"breakdown"`
	TotalInteractions int          `json:"totalInteractions" bson:"totalInteractions"`
	LastActivity      time.Time    `json:"lastActivity" bson:"lastActivity"`
}
