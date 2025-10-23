package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gen "bitbucket.org/dromos/memory-os/api/grpc/gen/api/grpc"
	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	gen.UnimplementedMemoryServiceServer // Embed for forward compatibility
	config                               *config.Config
	stmStore                             *memory.STMStore
	mongoClient                          *mongo.Client
	// Add other memory components as needed
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

	// Initialize STM Store (Milvus is optional and handled inside NewSTMStore)
	stmStore := memory.NewSTMStore(db, redisClient)

	server := &MemoryServer{
		config:      cfg,
		stmStore:    stmStore,
		mongoClient: mongoClient,
	}

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

// gRPC Method Implementations (Placeholders)

func (s *MemoryServer) CreateSession(ctx context.Context, req *gen.CreateSessionRequest) (*gen.CreateSessionResponse, error) {
	// Extract tenant and user ID from the request or context
	// For now, we'll use the request values directly.
	// In a real scenario, these should be validated against auth context.
	tenantID := req.TenantId
	userID := req.UserId
	agentID := req.AgentId

	if tenantID == "" || userID == "" || agentID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id, user_id, and agent_id are required")
	}

	// Generate a new unique chain ID
	chainID := fmt.Sprintf("chain_%s_%d", userID, time.Now().UnixNano())

	// Create a new DialogueChain document
	newChain := &models.DialogueChain{
		TenantID:   tenantID,
		UserID:     userID,
		AgentID:    agentID,
		ChainID:    chainID,
		Topic:      "New Session",
		Summary:    "Session initiated via CreateSession API.",
		StartedAt:  time.Now(),
		LastTurnAt: time.Now(),
		TurnCount:  0, // Starts with 0 turns
		Status:     "active",
	}

	// Insert into MongoDB
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("dialogue_chains")
	_, err := collection.InsertOne(ctx, newChain)
	if err != nil {
		log.Printf("ERROR: Failed to create new dialogue chain: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create session")
	}

	log.Printf("INFO: New session created - TenantID: %s, UserID: %s, SessionID: %s", tenantID, userID, chainID)

	// Return the new session ID
	return &gen.CreateSessionResponse{
		SessionId: chainID,
		// You can populate other fields like CreatedAt if they are added to the proto definition
	}, nil
}

func (s *MemoryServer) GetSession(ctx context.Context, req *gen.GetSessionRequest) (*gen.GetSessionResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetSession not implemented")
}

func (s *MemoryServer) DeleteSession(ctx context.Context, req *gen.DeleteSessionRequest) (*gen.DeleteSessionResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteSession not implemented")
}

func (s *MemoryServer) StoreInteraction(ctx context.Context, req *gen.StoreInteractionRequest) (*gen.StoreInteractionResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method StoreInteraction not implemented")
}

func (s *MemoryServer) GetContext(ctx context.Context, req *gen.GetContextRequest) (*gen.GetContextResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetContext not implemented")
}

func (s *MemoryServer) SearchMemory(ctx context.Context, req *gen.SearchMemoryRequest) (*gen.SearchMemoryResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SearchMemory not implemented")
}

func (s *MemoryServer) AnalyzeTopics(ctx context.Context, req *gen.AnalyzeTopicsRequest) (*gen.AnalyzeTopicsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AnalyzeTopics not implemented")
}

func (s *MemoryServer) GetHeatMetrics(ctx context.Context, req *gen.GetHeatMetricsRequest) (*gen.GetHeatMetricsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetHeatMetrics not implemented")
}

func (s *MemoryServer) GetSegments(ctx context.Context, req *gen.GetSegmentsRequest) (*gen.GetSegmentsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetSegments not implemented")
}

func (s *MemoryServer) HealthCheck(ctx context.Context, req *gen.HealthCheckRequest) (*gen.HealthCheckResponse, error) {
	// A simple health check implementation
	return &gen.HealthCheckResponse{Status: "healthy"}, nil
}
