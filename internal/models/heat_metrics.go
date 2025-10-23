package models

import "time"

// HeatMetrics represents aggregated heat metrics for a session or user
type HeatMetrics struct {
	OverallHeat       float64      `json:"overallHeat" bson:"overallHeat"`
	Breakdown         *HeatFactors `json:"breakdown" bson:"breakdown"`
	TotalInteractions int          `json:"totalInteractions" bson:"totalInteractions"`
	LastActivity      time.Time    `json:"lastActivity" bson:"lastActivity"`
}
