package models

import "time"

// HeatFactors represents the breakdown of heat score components
type HeatFactors struct {
	BaseImportance float64 `json:"baseImportance" bson:"baseImportance"` // I_base = w1*Intrinsic + w2*Cognitive
	TimeDecay      float64 `json:"timeDecay" bson:"timeDecay"`           // e^(-DeltaT / (Tau * S))
	RecallStrength float64 `json:"recallStrength" bson:"recallStrength"` // S (Stability factor)
}

// HeatMetrics represents aggregated heat metrics for a session or user
type HeatMetrics struct {
	OverallHeat       float64      `json:"overallHeat" bson:"overallHeat"`
	Breakdown         *HeatFactors `json:"breakdown" bson:"breakdown"`
	TotalInteractions int          `json:"totalInteractions" bson:"totalInteractions"`
	LastActivity      time.Time    `json:"lastActivity" bson:"lastActivity"`
}
