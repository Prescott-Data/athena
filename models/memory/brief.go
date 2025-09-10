package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// VisualizationProperties represents the properties of a visualization chart
// It accepts any structure as-is from the agent without transformation
type VisualizationProperties map[string]interface{}

// Visualization represents a visualization object with title, description and properties
type Visualization struct {
	Title       string                  `json:"title" bson:"title"`
	Description string                  `json:"description" bson:"description"`
	Properties  VisualizationProperties `json:"properties" bson:"properties"`
}

// Metric represents a KPI or metric item
type Metric struct {
	Title              string `json:"title" bson:"title"`
	KPIValue           string `json:"kpi_value" bson:"kpi_value"`
	InsightName        string `json:"insight_name" bson:"insight_name"`
	InsightDescription string `json:"insight_description" bson:"insight_description"`
}

// RecommendationsItem represents a single recommendation item with text.
type RecommendationsItem struct {
	Text string `json:"text" bson:"text"`
}

// SuggestedChannel represents a suggested channel/workspace from AI analysis
type SuggestedChannel struct {
	Name        string `json:"name" bson:"name"`
	Description string `json:"description" bson:"description"`
}

// SpotlightDataItem represents dynamic key-value pairs in spotlight data
type SpotlightDataItem map[string]interface{}

// ChannelSpotlightItem represents a single spotlight item for channels overview
type ChannelSpotlightItem struct {
	Title       string              `json:"title" bson:"title"`
	Description string              `json:"description" bson:"description"`
	Data        []SpotlightDataItem `json:"data" bson:"data"`
}

// Spotlight represents a document in the spotlights collection
type Spotlight struct {
	ID          primitive.ObjectID  `json:"id,omitempty" bson:"_id,omitempty"`
	BriefID     primitive.ObjectID  `json:"brief_id" bson:"brief_id"`
	Title       string              `json:"title" bson:"title"`
	Description string              `json:"description" bson:"description"`
	Data        []SpotlightDataItem `json:"data" bson:"data"`
	CreatedAt   time.Time           `json:"created_at" bson:"created_at"`
}

// DataItem represents a single analytical item with insights and optional visualization.
type DataItem struct {
	SummaryInsight            string                `json:"summary_insight" bson:"summary_insight"`
	Insight                   string                `json:"insight" bson:"insight"`
	Recommendations           []RecommendationsItem `json:"recommendations" bson:"recommendations"`
	PotentialImpact           string                `json:"potential_impact" bson:"potential_impact"`
	RecommendationsConfidence float64               `json:"recommendations_confidence" bson:"recommendations_confidence"`
	MoreDetails               string                `json:"more_details" bson:"more_details"`
	Visualization             Visualization         `json:"visualization" bson:"visualization"`
	SuggestedChannel          *SuggestedChannel     `json:"suggested_channel,omitempty" bson:"suggested_channel,omitempty"`
	Highlighted               bool                  `json:"highlighted" bson:"highlighted"`
}

// Brief represents a collection of insights or data items,
// created by the AI Agent for summarizing information.
type Brief struct {
	ID               primitive.ObjectID     `json:"id,omitempty" bson:"_id,omitempty"`
	Title            string                 `json:"title" bson:"title"`
	Data             []DataItem             `json:"data" bson:"data"`
	Metrics          []Metric               `json:"metrics" bson:"metrics"`
	Pinned           bool                   `json:"pinned" bson:"pinned"`
	ChannelSpotlight []ChannelSpotlightItem `json:"channel_spotlight,omitempty" bson:"channel_spotlight,omitempty"`
	CreatedAt        time.Time              `json:"created_at" bson:"created_at"`
	Collaborators    []Collaborator         `json:"collaborators,omitempty" bson:"collaborators,omitempty"`
	Feedback         []Feedback             `json:"feedback,omitempty" bson:"feedback,omitempty"`
	ChatMessages     []ChatMessage          `json:"chat_messages,omitempty" bson:"chat_messages,omitempty"`
}

// CollaboratorRequest represents the simplified payload for adding collaborators
type CollaboratorRequest struct {
	UserID string `json:"user_id" bson:"user_id" binding:"required"`
}

// Collaborator defines a user associated with a brief and their permissions.
type Collaborator struct {
	UserID  string    `json:"user_id" bson:"user_id"`
	Name    string    `json:"name" bson:"name"`
	Email   string    `json:"email" bson:"email"`
	Role    string    `json:"role" bson:"role"`
	AddedAt time.Time `json:"added_at" bson:"added_at"`
	BriefID string    `json:"brief_id" bson:"brief_id"`
}

// FeedbackUser represents the user who provided feedback
type FeedbackUser struct {
	UserID string `json:"user_id" bson:"user_id"`
	Name   string `json:"name" bson:"name"`
	Role   string `json:"role" bson:"role"`
}

// FeedbackRequest represents the simplified payload for creating feedback
type FeedbackRequest struct {
	UserID  string `json:"user_id" bson:"user_id" binding:"required"`
	Comment string `json:"comment" bson:"comment" binding:"required"`
}

// Feedback represents user-submitted comments on a brief.
type Feedback struct {
	ID        primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	BriefID   string             `json:"brief_id" bson:"brief_id"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time          `json:"updated_at" bson:"updated_at"`
	User      FeedbackUser       `json:"user" bson:"user"`
	Comment   string             `json:"comment" bson:"comment"`
}

// ChatMessage represents a single chat message within the context of a brief.
type ChatMessage struct {
	ID        primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	Author    string             `json:"author" bson:"author"`
	Message   string             `json:"message" bson:"message"`
	Timestamp time.Time          `json:"timestamp" bson:"timestamp"`
	BriefID   string             `json:"brief_id" bson:"brief_id"`
}

// DataItemResponse represents a DataItem for API responses without suggested_channel
type DataItemResponse struct {
	SummaryInsight            string                `json:"summary_insight"`
	Insight                   string                `json:"insight"`
	Recommendations           []RecommendationsItem `json:"recommendations"`
	PotentialImpact           string                `json:"potential_impact"`
	RecommendationsConfidence float64               `json:"recommendations_confidence"`
	MoreDetails               string                `json:"more_details"`
	Visualization             Visualization         `json:"visualization"`
	Highlighted               bool                  `json:"highlighted"`
}

// BriefListResponse represents a Brief for listing API responses without suggested_channel
type BriefListResponse struct {
	ID            primitive.ObjectID   `json:"id,omitempty"`
	Title         string               `json:"title"`
	Data          []DataItemResponse   `json:"data"`
	Metrics       []Metric             `json:"metrics"`
	Pinned        bool                 `json:"pinned"`
	CreatedAt     time.Time            `json:"created_at"`
	Collaborators []Collaborator       `json:"collaborators,omitempty"`
	Feedback      []Feedback           `json:"feedback,omitempty"`
	ChatMessages  []ChatMessage        `json:"chat_messages,omitempty"`
}
