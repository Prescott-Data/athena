package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	gen "bitbucket.org/dromos/memory-os/api/grpc/gen"
	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// stmCache defines the interface for the short-term memory cache.
// This allows for mocking in tests.
type stmCache interface {
	AddSTMEvent(ctx context.Context, userID string, event memory.STMEvent) error
}

// taskQueue defines the interface for the background task queue.
// This allows for mocking in tests.
type taskQueue interface {
	EnqueueCognitiveCheckTask(ctx context.Context, tenantID, userID, agentID string) error
}

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	gen.UnimplementedMemoryServiceServer // Embed for forward compatibility
	config                               *config.Config
	stmStore                             *memory.STMStore
	stmCache                             stmCache
	taskQueue                            taskQueue
	mongoClient                          *mongo.Client
	// getIDsFromSessionFunc allows mocking the DB call in tests
	getIDsFromSessionFunc func(ctx context.Context, s *MemoryServer, sessionID string) (string, string, string, error)
}

// NewMemoryServer creates a new Memory OS server instance
func NewMemoryServer(cfg *config.Config) (*MemoryServer, error) {
	// Initialize database connections
	mongoClient, db, err := database.ConnectMongoDB(database.ConnectionConfig{
		URI:            cfg.Database.MongoDB.URI,
		DatabaseName:   cfg.Database.MongoDB.Database,
		ConnectTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	redisClient, err := cache.NewRedisClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Initialize memory components
	stmStore := memory.NewSTMStore(db, redisClient)
	stmCache := memory.NewSTMCache(redisClient)
	taskQueue := memory.NewTaskQueue(redisClient)

	server := &MemoryServer{
		config:      cfg,
		stmStore:    stmStore,
		stmCache:    stmCache,
		taskQueue:   taskQueue,
		mongoClient: mongoClient,
	}
	// Point the function field to the real method
	server.getIDsFromSessionFunc = (*MemoryServer).getIDsFromSession

	return server, nil
}

// Close gracefully shuts down the server and cleans up resources
func (s *MemoryServer) Close() error {
	// Close MongoDB connection
	if s.mongoClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.mongoClient.Disconnect(ctx); err != nil {
			log.Printf("Error closing MongoDB connection: %v", err)
		}
	}

	return nil
}

// GetConfig returns the server configuration
func (s *MemoryServer) GetConfig() *config.Config {
	return s.config
}

// StoreInteraction is the main entry point for new events.
func (s *MemoryServer) StoreInteraction(ctx context.Context, req *gen.StoreInteractionRequest) (*gen.StoreInteractionResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(ctx, s, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	// Create and process the user event
	userEvent := memory.STMEvent{
		Role:      "user",
		Type:      models.STMEventTypeMessage,
		Content:   req.UserMessage,
		Timestamp: time.Now(),
	}

	// Always save the user event to the STM cache
	if err := s.stmCache.AddSTMEvent(ctx, userID, userEvent); err != nil {
		log.Printf("ERROR: Failed to add user event to STM cache: %v", err)
	}

	// Conditionally trigger the worker only for user messages
	if err := s.taskQueue.EnqueueCognitiveCheckTask(ctx, tenantID, userID, agentID); err != nil {
		log.Printf("ERROR: Failed to enqueue cognitive check task: %v", err)
	}

	// Create and process the agent event if it exists
	if req.AgentResponse != "" {
		agentEvent := memory.STMEvent{
			Role:      "agent",
			Type:      models.STMEventTypeMessage,
			Content:   req.AgentResponse,
			Timestamp: time.Now(),
		}
		if err := s.stmCache.AddSTMEvent(ctx, userID, agentEvent); err != nil {
			log.Printf("ERROR: Failed to add agent event to STM cache: %v", err)
		}
	}

	log.Printf("INFO: Interaction processed successfully for SessionID: %s", sessionID)

	return &gen.StoreInteractionResponse{
		Success:       true,
		InteractionId: "", // The concept of a single interaction ID is now obsolete
	}, nil
}

// getIDsFromSession retrieves tenant, user, and agent IDs from a session ID.
func (s *MemoryServer) getIDsFromSession(ctx context.Context, sessionID string) (string, string, string, error) {
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("cognitive_chains")
	var chain models.CognitiveChain
	err := collection.FindOne(ctx, bson.M{"chainId": sessionID}).Decode(&chain)
	if err != nil {
		return "", "", "", err
	}
	return chain.TenantID, chain.UserID, chain.AgentID, nil
}

// GetContext retrieves the recent conversation context
func (s *MemoryServer) GetContext(ctx context.Context, req *gen.GetContextRequest) (*gen.GetContextResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetContext not implemented")
}
