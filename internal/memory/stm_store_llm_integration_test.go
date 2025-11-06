package memory

import (
	"context"
	"fmt"
	"os"
	"testing"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestSTMStore_LLMIntegration tests the STM store LLM functionality.
// It assumes that the LLM_BASE_URL environment variable is set to a compatible LLM endpoint.
func TestSTMStore_LLMIntegration(t *testing.T) {
	assert := assert.New(t)

	// Skip test if not in integration test environment
	if os.Getenv("RUN_LLM_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping LLM integration test. Set RUN_LLM_INTEGRATION_TESTS=true to run.")
	}

	if os.Getenv("LLM_BASE_URL") == "" {
		t.Skip("Skipping LLM integration test: LLM_BASE_URL not set.")
	}

	ctx := context.Background()

	// Setup test database and cache
	mongoClient, err := setupTestMongoDB(ctx)
	if err != nil {
		t.Fatalf("Failed to setup test MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(ctx)

	redisClient, err := setupTestRedisClient()
	if err != nil {
		t.Fatalf("Failed to setup test Redis: %v", err)
	}
	defer redisClient.Close()

	// Create STM store with test clients
	db := mongoClient.Database("test_docintel_llm")
	stmStore := NewSTMStore(db, redisClient)

	// Test topic continuity analysis
	t.Run("TopicContinuityAnalysis", func(t *testing.T) {
		// Test case 1: Continuous topic (same subject)
		previousContent1 := "User: What's the weather like today?\nAgent: It's sunny and 75 degrees."
		newContent1 := "User: Will it rain later?\nAgent: There's a 20% chance of rain this afternoon."

		isContinuous, err := stmStore.analyzeTopicContinuity(ctx, "test-user", previousContent1, newContent1)
		assert.NoError(err)
		assert.True(isContinuous, "Weather-related questions should be continuous")

		// Test case 2: Topic change
		previousContent2 := "User: What's the weather like today?\nAgent: It's sunny and 75 degrees."
		newContent2 := "User: How do I bake a cake?\nAgent: Start by preheating your oven to 350°F."

		isContinuous, err = stmStore.analyzeTopicContinuity(ctx, "test-user", previousContent2, newContent2)
		assert.NoError(err)
		assert.False(isContinuous, "Weather to baking should be a topic change")
	})
}

// setupTestMongoDB creates a test MongoDB connection
func setupTestMongoDB(ctx context.Context) (*mongo.Client, error) {
	// Set test environment variables if not already set
	if os.Getenv("MONGO_URI") == "" {
		os.Setenv("MONGO_URI", "mongodb://dev:password1234@localhost:27017/?retryWrites=true&authSource=admin")
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("MONGO_URI")))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Test the connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	return client, nil
}

// setupTestRedisClient creates a test Redis client (reuse from other integration test)
func setupTestRedisClient() (cache.Interface, error) {
	// Set environment variables for testing if not already set
	if os.Getenv("REDIS_HOST") == "" {
		os.Setenv("REDIS_HOST", "localhost")
	}
	if os.Getenv("REDIS_PORT") == "" {
		os.Setenv("REDIS_PORT", "6379")
	}
	if os.Getenv("REDIS_DB") == "" {
		os.Setenv("REDIS_DB", "2") // Use DB 2 for LLM tests
	}
	if os.Getenv("REDIS_POOL_SIZE") == "" {
		os.Setenv("REDIS_POOL_SIZE", "10")
	}
	if os.Getenv("REDIS_POOL_TIMEOUT") == "" {
		os.Setenv("REDIS_POOL_TIMEOUT", "30")
	}
	if os.Getenv("CACHE_TTL") == "" {
		os.Setenv("CACHE_TTL", "3600")
	}

	// Use the existing NewRedisClient function
	client, err := cache.NewRedisClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	// Test the connection
	err = client.Health()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return client, nil
}