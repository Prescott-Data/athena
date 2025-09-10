package models

import "time"

// Conversation represents a conversation in the system.
type Conversation struct {
	ID             string    `json:"id" bson:"_id"`
	TenantID       string    `json:"tenantId" bson:"tenantId"`
	UserID         string    `json:"userId" bson:"userId"`
	AgentID        string    `json:"agentId" bson:"agentId"`
	Title          string    `json:"title" bson:"title"`
	Status         string    `json:"status" bson:"status"`
	RelatedFileIds []string  `json:"relatedFileIds" bson:"relatedFileIds"`
	MessagesCount  int       `json:"messagesCount" bson:"messagesCount"`
	CreatedAt      time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt" bson:"updatedAt"`
	LastMessageAt  time.Time `json:"lastMessageAt" bson:"lastMessageAt"`
}
