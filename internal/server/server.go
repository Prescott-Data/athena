package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dromos-org/memory-os/internal/cache"
	"github.com/dromos-org/memory-os/internal/config"
	"github.com/dromos-org/memory-os/internal/database"
	"github.com/dromos-org/memory-os/internal/memory"
	"go.mongodb.org/mongo-driver/mongo"
)

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	config      *config.Config
	stmStore    *memory.STMStore
	mongoClient *mongo.Client
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

// Health check
func (s *MemoryServer) HealthCheck(ctx context.Context) map[string]string {
	status := make(map[string]string)

	// Check STM Store
	if s.stmStore != nil {
		status["stm_store"] = "healthy"
	} else {
		status["stm_store"] = "unhealthy"
	}

	// Add other health checks here
	status["server"] = "healthy"

	return status
}

// GetConfig returns the server configuration
func (s *MemoryServer) GetConfig() *config.Config {
	return s.config
}
