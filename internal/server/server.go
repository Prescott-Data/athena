package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dromos-org/memory-os/internal/config"
	"github.com/dromos-org/memory-os/internal/database"
	"github.com/dromos-org/memory-os/internal/memory"
)

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	config      *config.Config
	stmStore    *memory.STMStore
	// Add other memory components as needed
}

// NewMemoryServer creates a new Memory OS server instance
func NewMemoryServer(cfg *config.Config) (*MemoryServer, error) {
	// Initialize database connections
	mongoClient, err := database.ConnectMongoDB(&database.ConnectionConfig{
		URI:         cfg.Database.MongoDB.URI,
		Database:    cfg.Database.MongoDB.Database,
		MaxPoolSize: 10,
		Timeout:     30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	redisClient, err := database.ConnectRedis(&database.RedisConfig{
		Host:     cfg.Database.Redis.Host,
		Port:     cfg.Database.Redis.Port,
		Password: cfg.Database.Redis.Password,
		DB:       cfg.Database.Redis.DB,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	milvusClient, err := database.ConnectMilvus(&database.MilvusConfig{
		Host: cfg.Database.Milvus.Host,
		Port: cfg.Database.Milvus.Port,
	})
	if err != nil {
		// Log warning but don't fail - milvus is optional for basic operations
		log.Printf("Warning: Failed to connect to Milvus: %v", err)
	}

	// Initialize STM Store
	stmStore, err := memory.NewSTMStore(mongoClient, redisClient, milvusClient, &memory.STMConfig{
		CacheMaxTurns: cfg.Memory.STM.CacheMaxTurns,
		CacheTTL:      cfg.Memory.STM.CacheTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize STM store: %w", err)
	}

	server := &MemoryServer{
		config:   cfg,
		stmStore: stmStore,
	}

	return server, nil
}

// Close gracefully shuts down the server and cleans up resources
func (s *MemoryServer) Close() error {
	// Close memory components
	if s.stmStore != nil {
		if err := s.stmStore.Close(); err != nil {
			log.Printf("Error closing STM store: %v", err)
		}
	}

	// Close database connections would go here
	
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
