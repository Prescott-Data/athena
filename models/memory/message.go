package models

import "time"

// Message represents a chat message in the system.
type Message struct {
	ID             string                 `json:"id" bson:"_id"`
	ConversationID string                 `json:"conversationId" bson:"conversationId"`
	Type           string                 `json:"type" bson:"type"`
	Content        string                 `json:"content" bson:"content"`
	Metadata       map[string]interface{} `json:"metadata,omitempty" bson:"metadata,omitempty"`
	CreatedAt      time.Time              `json:"createdAt" bson:"createdAt"`
}
