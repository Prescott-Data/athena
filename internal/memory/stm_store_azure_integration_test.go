package memory

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAzureEmbedding_Integration validates the Azure OpenAI embedding integration
func TestAzureEmbedding_Integration(t *testing.T) {
	assert := assert.New(t)

	// Check if Azure OpenAI is configured
	if os.Getenv("AZURE_OPENAI_ENDPOINT") == "" {
		t.Skip("Skipping Azure integration test: AZURE_OPENAI_ENDPOINT not configured")
	}

	// Create a mock STM store with minimal dependencies
	stmStore := createMockSTMStore(t)
	if stmStore == nil {
		t.Skip("Failed to create mock STM store for testing")
	}

	ctx := context.Background()

	t.Run("CreateEmbedding_Success", func(t *testing.T) {
		// Test data
		textToEmbed := "User: Hello, how are you today?\nAgent: I'm doing great! How can I help you?"

		// Call the CreateEmbedding function
		embedding, err := stmStore.CreateEmbedding(ctx, textToEmbed)

		// Validate no error occurred
		assert.NoError(err, "CreateEmbedding should not return an error")
		assert.NotNil(embedding, "Embedding should not be nil")

		// Validate embedding properties
		assert.NotEmpty(embedding.Vector, "Embedding vector should not be empty")
		assert.Equal(1536, embedding.Dimensions, "Embedding should have 1536 dimensions")
		assert.Equal(len(embedding.Vector), embedding.Dimensions, "Vector length should match dimensions")
		assert.NotEmpty(embedding.Model, "Model should be specified")
		assert.False(embedding.CreatedAt.IsZero(), "CreatedAt should be set")

		// Validate vector values are reasonable (not all zeros)
		nonZeroCount := 0
		for _, val := range embedding.Vector {
			if val != 0.0 {
				nonZeroCount++
			}
		}
		assert.Greater(nonZeroCount, 100, "Most vector values should be non-zero")

		// Validate model name contains expected value
		assert.Contains(embedding.Model, "text-embedding", "Model should be a text embedding model")

		t.Logf("SUCCESS: Created embedding with %d dimensions using model '%s'",
			embedding.Dimensions, embedding.Model)
	})

	t.Run("CreateEmbedding_EmptyInput", func(t *testing.T) {
		// Test with empty input
		embedding, err := stmStore.CreateEmbedding(ctx, "")

		// Should still work (OpenAI handles empty input)
		assert.NoError(err, "CreateEmbedding should handle empty input")
		assert.NotNil(embedding, "Embedding should not be nil even with empty input")
		assert.Equal(1536, embedding.Dimensions, "Embedding should still have 1536 dimensions")
	})

	t.Run("CreateEmbedding_LongInput", func(t *testing.T) {
		// Test with longer input to validate token handling
		longText := "User: This is a much longer message that contains multiple sentences and various topics. " +
			"We want to test how the embedding model handles longer text inputs and whether it properly " +
			"processes the content to create meaningful vector representations. This should help us " +
			"validate that our integration works correctly with various input sizes.\n" +
			"Agent: Thank you for that detailed message. I understand you're testing the embedding " +
			"functionality with longer text inputs. This is indeed a good approach to validate that the " +
			"OpenAI integration can handle various message lengths and still produce consistent, high-quality " +
			"embeddings that will be useful for similarity matching and retrieval in your memory system."

		embedding, err := stmStore.CreateEmbedding(ctx, longText)

		assert.NoError(err, "CreateEmbedding should handle long input")
		assert.NotNil(embedding, "Embedding should not be nil")
		assert.Equal(1536, embedding.Dimensions, "Embedding should have 1536 dimensions")

		t.Logf("SUCCESS: Processed long input (%d chars) into embedding", len(longText))
	})
}

// TestGoogleConfiguration_Validation validates environment configuration
func TestGoogleConfiguration_Validation(t *testing.T) {
	assert := assert.New(t)

	t.Run("EnvironmentVariables_Loaded", func(t *testing.T) {
		// Test that environment variables are properly loaded
		EmbeddingModelName := os.Getenv("EMBEDDING_MODEL_NAME")
		EmbeddingDimensions := os.Getenv("EMBEDDING_DIMENSIONS")
		azureEndpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
		// Note: API key is retrieved from Secret Manager, not environment

		// EmbeddingModelName can be empty (has default)
		if EmbeddingModelName != "" {
			assert.Contains(EmbeddingModelName, "embedding", "EmbeddingModelName should be an embedding model")
		}

		assert.Equal("1536", EmbeddingDimensions, "EmbeddingDimensions should be 1536")

		if azureEndpoint == "" {
			azureEndpoint = "not-configured"
		}

		t.Logf("Configuration: Model=%s, Dimensions=%s, AzureEndpoint=%s",
			EmbeddingModelName, EmbeddingDimensions, azureEndpoint)
	})
}

// TestAzureEmbedding_Performance validates response time
func TestAzureEmbedding_Performance(t *testing.T) {
	assert := assert.New(t)

	// Check if Azure OpenAI is configured

	stmStore := createMockSTMStore(t)
	if stmStore == nil {
		t.Skip("Failed to create mock STM store for testing")
	}

	ctx := context.Background()

	t.Run("EmbeddingLatency_Reasonable", func(t *testing.T) {
		textToEmbed := "User: Performance test message\nAgent: This is a test response for performance validation"

		start := time.Now()
		embedding, err := stmStore.CreateEmbedding(ctx, textToEmbed)
		duration := time.Since(start)

		assert.NoError(err, "CreateEmbedding should succeed")
		assert.NotNil(embedding, "Embedding should not be nil")

		// Reasonable latency expectation (adjust based on your requirements)
		assert.Less(duration, 10*time.Second, "Embedding creation should complete within 10 seconds")

		t.Logf("Embedding created in %v", duration)
	})
}

// createMockSTMStore creates a minimal STM store for testing (without full dependencies)
func createMockSTMStore(t *testing.T) *STMStore {
	t.Helper()
	// Create a minimal STM store without Redis/MongoDB dependencies for embedding testing
	// Since CreateEmbedding only needs the struct, we can create a minimal instance

	return &STMStore{
		db:     nil, // Not needed for embedding test
		redis:  nil, // Not needed for embedding test
		milvus: nil, // Not needed for embedding test
		llmGuards: &LLMGuardrails{
			redis: nil, // Not needed for this test
		},
		HTTPClient: &http.Client{},
	}
}

// // setupTestDatabase creates a test MongoDB connection (optional, for full integration)
// func setupTestDatabase() (*mongo.Database, error) {
// 	// Use test database if needed for full integration testing
// 	mongoURI := os.Getenv("MONGODB_URI")
// 	if mongoURI == "" {
// 		mongoURI = "mongodb://localhost:27017"
// 	}

// 	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
// 	if err != nil {
// 		return nil, err
// 	}

// 	return client.Database("test_docintel"), nil
// }

// setupTestRedis creates a test Redis connection (optional, for full integration)
// func setupTestRedis() (cache.Interface, error) {
// 	// Use test Redis if needed for full integration testing
// 	redisHost := os.Getenv("REDIS_HOST")
// 	if redisHost == "" {
// 		redisHost = "localhost"
// 	}

// 	redisPort := os.Getenv("REDIS_PORT")
// 	if redisPort == "" {
// 		redisPort = "6379"
// 	}

// 	// Set test environment
// 	os.Setenv("REDIS_HOST", redisHost)
// 	os.Setenv("REDIS_PORT", redisPort)
// 	os.Setenv("REDIS_DB", "15") // Use DB 15 for testing

// 	return cache.NewRedisClient()
// }
