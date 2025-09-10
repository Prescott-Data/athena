package database

import (
	"context"
	"fmt"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

// MilvusConfig holds Milvus connection configuration
type MilvusConfig struct {
	Host string
	Port int
}

// ConnectMilvus establishes a connection to Milvus vector database
func ConnectMilvus(config *MilvusConfig) (client.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := client.NewGrpcClient(ctx, fmt.Sprintf("%s:%d", config.Host, config.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Milvus: %w", err)
	}

	// Test the connection by checking if Milvus is healthy
	state, err := c.CheckHealth(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check Milvus health: %w", err)
	}

	if state == nil || !state.IsHealthy {
		return nil, fmt.Errorf("Milvus is not healthy")
	}

	return c, nil
}

// InitializeMilvusCollections creates the required collections for memory embeddings
func InitializeMilvusCollections(ctx context.Context, c client.Client) error {
	// This function would create the necessary collections for vector embeddings
	// For now, we'll keep it simple and just verify the connection works

	// Check if we can list collections (basic operation test)
	_, err := c.ListCollections(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Milvus collections: %w", err)
	}

	return nil
}
