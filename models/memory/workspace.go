package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RecommendationItem represents a single recommendation
type RecommendationItem struct {
	Text         string   `json:"text" bson:"text"`
	Rating       *float64 `json:"rating,omitempty" bson:"rating,omitempty"`
	HelpfulVotes *int     `json:"helpful_votes,omitempty" bson:"helpful_votes,omitempty"`
}

// CollaboratorInfo represents collaborator information in insights
type CollaboratorInfo struct {
	ID            string   `json:"id" bson:"id"`
	Name          string   `json:"name" bson:"name"`
	Role          string   `json:"role" bson:"role"`
	AvatarURL     string   `json:"avatar_url,omitempty" bson:"avatar_url,omitempty"`
	LatestComment string   `json:"latest_comment,omitempty" bson:"latest_comment,omitempty"`
	Attachments   []string `json:"attachments,omitempty" bson:"attachments,omitempty"`
}

// ConversationUser represents a user in a conversation
type ConversationUser struct {
	ID        string `json:"id" bson:"id"`
	Name      string `json:"name" bson:"name"`
	AvatarURL string `json:"avatar_url,omitempty" bson:"avatar_url,omitempty"`
	Role      string `json:"role,omitempty" bson:"role,omitempty"`
}

// ConversationMessage represents a conversation message
type ConversationMessage struct {
	ID          string           `json:"id" bson:"id"`
	User        ConversationUser `json:"user" bson:"user"`
	Message     string           `json:"message" bson:"message"`
	Timestamp   time.Time        `json:"timestamp" bson:"timestamp"`
	Attachments []string         `json:"attachments,omitempty" bson:"attachments,omitempty"`
}

// WorkspaceRef represents a reference to a workspace
type WorkspaceRef struct {
	ID   string `json:"id" bson:"id"`
	Name string `json:"name" bson:"name"`
}

// Insight represents an insight within a workspace
type Insight struct {
	ID                        primitive.ObjectID    `json:"_id,omitempty" bson:"_id,omitempty"`
	Title                     string                `json:"title" bson:"title"`
	Description               string                `json:"description" bson:"description"`
	Status                    string                `json:"status" bson:"status"`
	Highlighted               bool                  `json:"highlighted" bson:"highlighted"`
	CreatedAt                 time.Time             `json:"created_at" bson:"created_at"`
	UpdatedAt                 time.Time             `json:"updated_at" bson:"updated_at"`
	Metrics                   *Metric               `json:"metrics,omitempty" bson:"metrics,omitempty"`
	Visualization             *Visualization        `json:"visualization,omitempty" bson:"visualization,omitempty"`
	Recommendations           []RecommendationItem  `json:"recommendations,omitempty" bson:"recommendations,omitempty"`
	RecommendationsCount      *int                  `json:"recommendations_count,omitempty" bson:"recommendations_count,omitempty"`
	RecommendationsConfidence *float64              `json:"recommendations_confidence,omitempty" bson:"recommendations_confidence,omitempty"`
	Collaborators             []CollaboratorInfo    `json:"collaborators,omitempty" bson:"collaborators,omitempty"`
	CollaboratorsCount        *int                  `json:"collaborators_count,omitempty" bson:"collaborators_count,omitempty"`
	Conversations             []ConversationMessage `json:"conversations,omitempty" bson:"conversations,omitempty"`
	PotentialImpact           string                `json:"potential_impact,omitempty" bson:"potential_impact,omitempty"`
	Impact                    string                `json:"impact,omitempty" bson:"impact,omitempty"`
	Priority                  string                `json:"priority,omitempty" bson:"priority,omitempty"`
	DocumentsCount            int                   `json:"documents_count,omitempty" bson:"documents_count,omitempty"`
	Workspace                 *WorkspaceRef         `json:"workspace,omitempty" bson:"workspace,omitempty"`
	Feedback                  []InsightFeedback     `json:"feedback,omitempty" bson:"feedback,omitempty"`
}

// WorkspaceCollaborator represents a user collaborating on an insight in a workspace
type WorkspaceCollaborator struct {
	ID        primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	InsightID primitive.ObjectID `json:"insight_id" bson:"insight_id"`
	UserID    string             `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Email     string             `json:"email" bson:"email"`
	Role      string             `json:"role" bson:"role"`
	JoinedAt  time.Time          `json:"joined_at" bson:"joined_at"`
}

// Document represents a document associated with an insight
type Document struct {
	ID         primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	InsightID  primitive.ObjectID `json:"insight_id" bson:"insight_id"`
	Name       string             `json:"name" bson:"name"`
	Type       string             `json:"type" bson:"type"` // "pdf", "doc", "xlsx", etc.
	URL        string             `json:"url" bson:"url"`
	Size       int64              `json:"size" bson:"size"`
	UploadedBy string             `json:"uploaded_by" bson:"uploaded_by"`
	CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
}

// Alert represents an alert within a workspace
type Alert struct {
	ID          primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	WorkspaceID primitive.ObjectID `json:"workspace_id" bson:"workspace_id"`
	Title       string             `json:"title" bson:"title"`
	Message     string             `json:"message" bson:"message"`
	Severity    string             `json:"severity" bson:"severity"`
	Status      string             `json:"status" bson:"status"`
	TriggeredAt time.Time          `json:"triggered_at" bson:"triggered_at"`
	ResolvedAt  *time.Time         `json:"resolved_at,omitempty" bson:"resolved_at,omitempty"`
}

// WorkspaceMemberRequest represents the simplified payload for adding members to workspace
type WorkspaceMemberRequest struct {
	UserID string `json:"user_id" bson:"user_id" binding:"required"`
}

// WorkspaceMember represents a simplified member structure for workspaces
type WorkspaceMember struct {
	UserID string `json:"user_id" bson:"user_id"`
	Name   string `json:"name" bson:"name"`
	Role   string `json:"role" bson:"role"`
}

// Workspace represents a workspace containing multiple insights
type Workspace struct {
	ID          primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	Name        string             `json:"name" bson:"name"`
	Description string             `json:"description" bson:"description"`
	Members     []WorkspaceMember  `json:"members" bson:"members"`
	CreatedBy   string             `json:"created_by" bson:"created_by"`
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" bson:"updated_at"`
}

// InsightFeedbackUser represents the user who provided feedback on an insight
type InsightFeedbackUser struct {
	UserID string `json:"user_id" bson:"user_id"`
	Name   string `json:"name" bson:"name"`
	Role   string `json:"role" bson:"role"`
}

// InsightFeedbackRequest represents the simplified payload for creating insight feedback
type InsightFeedbackRequest struct {
	UserID  string `json:"user_id" bson:"user_id" binding:"required"`
	Comment string `json:"comment" bson:"comment" binding:"required"`
}

// InsightFeedback represents user-submitted comments on an insight
type InsightFeedback struct {
	ID        primitive.ObjectID  `json:"id,omitempty" bson:"_id,omitempty"`
	InsightID primitive.ObjectID  `json:"insight_id" bson:"insight_id"`
	CreatedAt time.Time           `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time           `json:"updated_at" bson:"updated_at"`
	User      InsightFeedbackUser `json:"user" bson:"user"`
	Comment   string              `json:"comment" bson:"comment"`
}

// InsightCollaboratorRequest represents the simplified payload for adding collaborators to insight
type InsightCollaboratorRequest struct {
	UserID string `json:"user_id" bson:"user_id" binding:"required"`
}

// InsightCollaborator represents a user collaborating on an insight
type InsightCollaborator struct {
	UserID    string    `json:"user_id" bson:"user_id"`
	Name      string    `json:"name" bson:"name"`
	Email     string    `json:"email" bson:"email"`
	Role      string    `json:"role" bson:"role"`
	AddedAt   time.Time `json:"added_at" bson:"added_at"`
	InsightID string    `json:"insight_id" bson:"insight_id"`
}

// WorkspaceResponse represents the response structure for workspace with insights
type WorkspaceResponse struct {
	ID            primitive.ObjectID `json:"_id,omitempty"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Members       []WorkspaceMember  `json:"members"`
	CreatedBy     string             `json:"created_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Insights      []Insight          `json:"insights,omitempty"`
	InsightsCount int                `json:"insights_count,omitempty"`
}
