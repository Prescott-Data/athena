package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dromos-org/memory-os/internal/cache"
	"github.com/dromos-org/memory-os/internal/models"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	GEMINI_API_KEY_SECRET = "dromos-gemini-api-key"
	SERVICE_ACCOUNT       = "dromos-ccm-sa@dromos-nonprod-svc-gt8n.iam.gserviceaccount.com"
	PROJECT_ID            = "dromos-nonprod-svc-gt8n" // Extracted from service account
)

// GeminiResponse represents the response from Gemini API
type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts,omitempty"`
			Role string `json:"role,omitempty"`
		} `json:"content"`
		FinishReason string `json:"finishReason,omitempty"`
	} `json:"candidates"`
}

// GeminiRequest represents a request to Gemini API
type GeminiRequest struct {
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	GenerationConfig struct {
		Temperature     float64 `json:"temperature"`
		MaxOutputTokens int     `json:"maxOutputTokens"`
	} `json:"generationConfig"`
}

// TestSTMStore_LLMIntegration tests STM store LLM functionality with real Gemini Flash
func TestSTMStore_LLMIntegration(t *testing.T) {
	assert := assert.New(t)

	// Skip test if not in integration test environment
	if os.Getenv("RUN_LLM_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping LLM integration test. Set RUN_LLM_INTEGRATION_TESTS=true to run.")
	}

	ctx := context.Background()

	// 1. Setup Google Cloud authentication and get API key
	apiKey, err := getGeminiAPIKeyFromSecrets(ctx)
	if err != nil {
		t.Fatalf("Failed to get Gemini API key from secrets: %v", err)
	}

	// 2. Setup test database and cache
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

	// 3. Create STM store with test clients
	db := mongoClient.Database("test_docintel")
	stmStore := NewSTMStore(db, redisClient)

	// 4. Test topic continuity analysis with Gemini
	t.Run("TopicContinuityAnalysis", func(t *testing.T) {
		// Create a previous dialogue page
		previousPage := &models.DialoguePage{
			UserMessage:   "What's the weather like today?",
			AgentResponse: "It's sunny and 75 degrees.",
		}

		// Test case 1: Continuous topic (same subject)
		userMessage1 := "Will it rain later?"
		agentResponse1 := "There's a 20% chance of rain this afternoon."

		isContinuous, err := analyzeTopicContinuityWithGemini(ctx, apiKey, previousPage, userMessage1, agentResponse1)
		assert.NoError(err)
		assert.True(isContinuous, "Weather-related questions should be continuous")

		// Test case 2: Topic change
		userMessage2 := "How do I bake a cake?"
		agentResponse2 := "Start by preheating your oven to 350°F."

		isContinuous, err = analyzeTopicContinuityWithGemini(ctx, apiKey, previousPage, userMessage2, agentResponse2)
		assert.NoError(err)
		assert.False(isContinuous, "Weather to baking should be a topic change")
	})

	// 5. Test embedding creation (mock for now, as we'd need embedding service)
	t.Run("EmbeddingCreation", func(t *testing.T) {
		userMessage := "Hello, how are you?"
		agentResponse := "I'm doing well, thank you!"

		// For now, test that the function properly handles missing embedding service
		embedding, err := stmStore.CreateEmbedding(ctx, userMessage, agentResponse)
		assert.Error(err) // Should error because EMBEDDING_SERVICE_URL not set
		assert.Nil(embedding)
		assert.Contains(err.Error(), "EMBEDDING_SERVICE_URL not configured")
	})

	// 6. Test full dialogue chain determination workflow
	t.Run("DialogueChainDetermination", func(t *testing.T) {
		userID := "test_llm_user_123"

		// First conversation turn - should create new chain
		chainID1, err := stmStore.DetermineDialogueChain(ctx, "test_tenant", userID, "test_agent", "What's the capital of France?", "The capital of France is Paris.")
		assert.NoError(err)
		assert.Contains(chainID1, userID)
		assert.Contains(chainID1, "chain_")

		// This test requires embedding service to be fully functional
		// For now, we validate that the logic flow works correctly
		t.Logf("Created new dialogue chain: %s", chainID1)
	})
}

// getGeminiAPIKeyFromSecrets retrieves the Gemini API key from Google Secrets Manager
func getGeminiAPIKeyFromSecrets(ctx context.Context) (string, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create secretmanager client: %w", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", PROJECT_ID, GEMINI_API_KEY_SECRET),
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}

	return string(result.Payload.Data), nil
}

// analyzeTopicContinuityWithGemini calls Gemini 2.5 Flash directly for topic continuity analysis
func analyzeTopicContinuityWithGemini(ctx context.Context, apiKey string, previousPage *models.DialoguePage, userMessage, agentResponse string) (bool, error) {
	// Create prompt for Gemini
	prompt := fmt.Sprintf(`Are these two conversation turns about the same topic?

Turn 1: User: %s / Assistant: %s
Turn 2: User: %s / Assistant: %s

Answer only "true" or "false".`,
		previousPage.UserMessage, previousPage.AgentResponse, userMessage, agentResponse)

	// Prepare Gemini API request
	geminiReq := GeminiRequest{
		Contents: []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}{
			{
				Parts: []struct {
					Text string `json:"text"`
				}{
					{Text: prompt},
				},
			},
		},
		GenerationConfig: struct {
			Temperature     float64 `json:"temperature"`
			MaxOutputTokens int     `json:"maxOutputTokens"`
		}{
			Temperature:     0.1,
			MaxOutputTokens: 50000,
		},
	}

	requestBody, err := json.Marshal(geminiReq)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API call to Gemini 2.5 Flash
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", apiKey)

	httpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to make API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(responseBody, &geminiResp); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return false, fmt.Errorf("no candidates in Gemini response")
	}

	candidate := geminiResp.Candidates[0]

	// Check finish reason
	if candidate.FinishReason == "MAX_TOKENS" {
		return false, fmt.Errorf("Gemini response was truncated due to max tokens limit")
	}

	if len(candidate.Content.Parts) == 0 {
		return false, fmt.Errorf("no response parts from Gemini - finish reason: %s", candidate.FinishReason)
	}

	responseText := candidate.Content.Parts[0].Text

	// Parse response
	if responseText == "true" {
		return true, nil
	} else if responseText == "false" {
		return false, nil
	} else {
		return false, fmt.Errorf("unexpected response from Gemini: %s", responseText)
	}
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
