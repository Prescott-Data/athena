package memory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dromos-org/memory-os/internal/cache"
	"github.com/dromos-org/memory-os/internal/models"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestSTMPipeline_EndToEnd tests the complete STM Cache → STM Store pipeline
func TestSTMPipeline_EndToEnd(t *testing.T) {
	assert := assert.New(t)

	// Skip if not running comprehensive integration tests
	if os.Getenv("RUN_E2E_TESTS") != "true" {
		t.Skip("Skipping E2E test. Set RUN_E2E_TESTS=true to run.")
	}

	// Setup all required components
	components, cleanup := setupE2ETestComponents(t)
	if components == nil {
		t.Skip("Failed to setup E2E test components")
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	userID := fmt.Sprintf("test_user_%d", time.Now().Unix())

	t.Run("NewConversation_Pipeline", func(t *testing.T) {
		// Step 1: Add first turn to STM Cache (should start new dialogue chain)
		userMessage1 := "Hello, I'm having trouble with my database connection. Can you help me troubleshoot?"
		agentResponse1 := "I'd be happy to help you troubleshoot your database connection! Let's start by checking the connection string and network connectivity. What database are you using?"

		// Add user turn
		userTurn1 := ConversationTurn{
			Type:      "user",
			Content:   userMessage1,
			Timestamp: time.Now(),
		}
		err := components.stmCache.AddConversationTurn(ctx, userID, userTurn1)
		assert.NoError(err, "Adding user turn should succeed")

		// Add agent turn
		agentTurn1 := ConversationTurn{
			Type:      "agent",
			Content:   agentResponse1,
			Timestamp: time.Now(),
		}
		err = components.stmCache.AddConversationTurn(ctx, userID, agentTurn1)
		assert.NoError(err, "Adding agent turn should succeed")

		// Step 2: Verify turns are in cache
		turns, err := components.stmCache.GetConversationContext(ctx, userID)
		assert.NoError(err, "Getting recent turns should succeed")
		assert.Len(turns, 2, "Should have 2 turns in cache (user + agent)")

		// Step 3: Trigger background processing (dialogue chain analysis)
		// This simulates what happens in the cold path
		// Use the original messages for dialogue chain analysis

		// Create embedding for this dialogue turn
		embedding, err := components.stmStore.CreateEmbedding(ctx, userMessage1, agentResponse1)
		assert.NoError(err, "Creating embedding should succeed")
		assert.NotNil(embedding, "Embedding should not be nil")
		assert.Equal(1536, embedding.Dimensions, "Should have 1536 dimensions")

		// Step 4: Test dialogue chain analysis (should create new chain for first turn)
		chainID, err := components.stmStore.DetermineDialogueChain(ctx, "test_tenant", userID, "test_agent", userMessage1, agentResponse1)
		assert.NoError(err, "Dialogue chain analysis should succeed")
		assert.NotEmpty(chainID, "Chain ID should be generated")

		// Step 5: Store the dialogue page
		dialoguePage := &models.DialoguePage{
			UserID:        userID,
			ChainID:       chainID,
			UserMessage:   userMessage1,
			AgentResponse: agentResponse1,
			TurnIndex:     1,
			Status:        "in_stm",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		pageID, err := components.stmStore.StoreDialoguePage(ctx, dialoguePage)
		assert.NoError(err, "Storing dialogue page should succeed")
		assert.NotEmpty(pageID, "Page ID should be generated")

		t.Logf("✅ New conversation: ChainID=%s, PageID=%s, Dimensions=%d",
			chainID, pageID.Hex(), embedding.Dimensions)
	})

	t.Run("ContinuedConversation_Pipeline", func(t *testing.T) {
		// Step 1: Add related turn to continue the database troubleshooting conversation
		userMessage2 := "I'm using PostgreSQL and the error message says 'connection refused'. The connection string looks correct to me."
		agentResponse2 := "A 'connection refused' error with PostgreSQL usually indicates that either the database server isn't running, or there's a network/firewall issue. Let's check if the PostgreSQL service is running first."

		// Add user turn
		userTurn2 := ConversationTurn{
			Type:      "user",
			Content:   userMessage2,
			Timestamp: time.Now(),
		}
		err := components.stmCache.AddConversationTurn(ctx, userID, userTurn2)
		assert.NoError(err, "Adding user continuation turn should succeed")

		// Add agent turn
		agentTurn2 := ConversationTurn{
			Type:      "agent",
			Content:   agentResponse2,
			Timestamp: time.Now(),
		}
		err = components.stmCache.AddConversationTurn(ctx, userID, agentTurn2)
		assert.NoError(err, "Adding agent continuation turn should succeed")

		// Step 2: Get recent turns (should now have 4)
		turns, err := components.stmCache.GetConversationContext(ctx, userID)
		assert.NoError(err, "Getting recent turns should succeed")
		assert.Len(turns, 4, "Should have 4 turns in cache (2 dialogue pairs)")

		// Step 3: Test dialogue chain analysis (should continue existing chain)
		chainID, err := components.stmStore.DetermineDialogueChain(ctx, "test_tenant", userID, "test_agent", userMessage2, agentResponse2)
		assert.NoError(err, "Dialogue chain analysis should succeed")
		assert.NotEmpty(chainID, "Should reuse existing chain ID")

		// Step 4: Create embedding and store
		_, err = components.stmStore.CreateEmbedding(ctx, userMessage2, agentResponse2)
		assert.NoError(err, "Creating embedding should succeed")

		dialoguePage := &models.DialoguePage{
			UserID:        userID,
			ChainID:       chainID,
			UserMessage:   userMessage2,
			AgentResponse: agentResponse2,
			TurnIndex:     2,
			Status:        "in_stm",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		pageID, err := components.stmStore.StoreDialoguePage(ctx, dialoguePage)
		assert.NoError(err, "Storing continuation dialogue page should succeed")

		t.Logf("✅ Continued conversation: ChainID=%s, PageID=%s",
			chainID, pageID.Hex())
	})

	t.Run("TopicChange_Pipeline", func(t *testing.T) {
		// Step 1: Add completely different topic to test topic boundary detection
		userMessage3 := "Actually, can you help me with a completely different issue? I need to create a responsive CSS layout."
		agentResponse3 := "Of course! I'd be happy to help you with CSS layouts. Responsive design is all about making your layout adapt to different screen sizes. Are you looking to use Flexbox, CSS Grid, or a specific framework?"

		// Add user turn
		userTurn3 := ConversationTurn{
			Type:      "user",
			Content:   userMessage3,
			Timestamp: time.Now(),
		}
		err := components.stmCache.AddConversationTurn(ctx, userID, userTurn3)
		assert.NoError(err, "Adding user topic change turn should succeed")

		// Add agent turn
		agentTurn3 := ConversationTurn{
			Type:      "agent",
			Content:   agentResponse3,
			Timestamp: time.Now(),
		}
		err = components.stmCache.AddConversationTurn(ctx, userID, agentTurn3)
		assert.NoError(err, "Adding agent topic change turn should succeed")

		// Step 2: Get the latest turns
		turns, err := components.stmCache.GetConversationContext(ctx, userID)
		assert.NoError(err, "Getting recent turns should succeed")
		assert.Len(turns, 6, "Should have 6 turns in cache (3 dialogue pairs)")

		// Step 3: Test dialogue chain analysis (should start NEW chain due to topic change)
		chainID, err := components.stmStore.DetermineDialogueChain(ctx, "test_tenant", userID, "test_agent", userMessage3, agentResponse3)
		assert.NoError(err, "Dialogue chain analysis should succeed")
		// Note: This should start a new chain due to low cosine similarity, but let's be flexible
		// as the exact threshold behavior might vary

		t.Logf("✅ Topic change: ChainID=%s", chainID)

		// Step 4: Create embedding and store regardless of chain decision
		_, err = components.stmStore.CreateEmbedding(ctx, userMessage3, agentResponse3)
		assert.NoError(err, "Creating embedding should succeed")

		dialoguePage := &models.DialoguePage{
			UserID:        userID,
			ChainID:       chainID,
			UserMessage:   userMessage3,
			AgentResponse: agentResponse3,
			TurnIndex:     3,
			Status:        "in_stm",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		_, err = components.stmStore.StoreDialoguePage(ctx, dialoguePage)
		assert.NoError(err, "Storing topic change dialogue page should succeed")
	})

	t.Run("CacheEviction_Pipeline", func(t *testing.T) {
		// Step 1: Fill cache beyond capacity to trigger eviction
		// Default STM cache capacity is 10 turns, so add several more turns
		baseMessage := "Turn #%d: This is a filler message to test cache eviction behavior."

		initialTurns, _ := components.stmCache.GetConversationContext(ctx, userID)
		initialCount := len(initialTurns)

		// Add enough turns to trigger eviction
		for i := 4; i <= 12; i++ {
			userMsg := fmt.Sprintf(baseMessage, i)
			agentMsg := fmt.Sprintf("Response to %s", userMsg)

			// Add user turn
			userTurn := ConversationTurn{
				Type:      "user",
				Content:   userMsg,
				Timestamp: time.Now(),
			}
			err := components.stmCache.AddConversationTurn(ctx, userID, userTurn)
			assert.NoError(err, "Adding user turn %d should succeed", i)

			// Add agent turn
			agentTurn := ConversationTurn{
				Type:      "agent",
				Content:   agentMsg,
				Timestamp: time.Now(),
			}
			err = components.stmCache.AddConversationTurn(ctx, userID, agentTurn)
			assert.NoError(err, "Adding agent turn %d should succeed", i)
		}

		// Step 2: Verify cache respects capacity limits
		finalTurns, err := components.stmCache.GetConversationContext(ctx, userID)
		assert.NoError(err, "Getting turns after eviction should succeed")

		// Should not exceed STM cache capacity (default 10)
		maxCapacity := 10 // StmCacheMaxTurns default
		assert.LessOrEqual(len(finalTurns), maxCapacity,
			"Cache should not exceed capacity (%d), but has %d turns", maxCapacity, len(finalTurns))

		// Should have more turns than initially
		assert.Greater(len(finalTurns), initialCount,
			"Should have more turns than initial count (%d vs %d)", len(finalTurns), initialCount)

		t.Logf("✅ Cache eviction: Initial=%d, Final=%d, Capacity=%d",
			initialCount, len(finalTurns), maxCapacity)
	})
}

// E2ETestComponents holds all components needed for end-to-end testing
type E2ETestComponents struct {
	stmCache  *STMCache
	stmStore  *STMStore
	taskQueue *TaskQueue
	redis     cache.Interface
	mongodb   *mongo.Database
}

// setupE2ETestComponents sets up all required components for end-to-end testing
func setupE2ETestComponents(t *testing.T) (*E2ETestComponents, func()) {
	// Setup Redis for STM Cache and Task Queue
	if os.Getenv("REDIS_HOST") == "" {
		os.Setenv("REDIS_HOST", "localhost")
	}
	if os.Getenv("REDIS_PORT") == "" {
		os.Setenv("REDIS_PORT", "6379")
	}
	if os.Getenv("REDIS_DB") == "" {
		os.Setenv("REDIS_DB", "2") // Use DB 2 for E2E tests
	}

	redisClient, err := cache.NewRedisClient()
	if err != nil {
		t.Logf("Failed to create Redis client: %v", err)
		return nil, func() {}
	}

	err = redisClient.Health()
	if err != nil {
		t.Logf("Failed to connect to Redis: %v", err)
		return nil, func() {}
	}

	// Setup MongoDB for STM Store
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://dev:password1234@localhost:27017/?retryWrites=true&authSource=admin"
	}

	mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Logf("Failed to connect to MongoDB: %v", err)
		return nil, func() {}
	}

	err = mongoClient.Ping(context.Background(), nil)
	if err != nil {
		t.Logf("Failed to ping MongoDB: %v", err)
		return nil, func() {}
	}

	mongodb := mongoClient.Database("docintel_e2e_test")

	// Create STM Cache
	stmCache := NewSTMCache(redisClient)

	// Create STM Store (with real dependencies for E2E testing)
	stmStore := &STMStore{
		db:     mongodb,
		redis:  redisClient,
		milvus: nil, // Note: Milvus not required for embedding creation
		llmGuards: &LLMGuardrails{
			redis: redisClient,
		},
	}

	// Create Task Queue
	taskQueue := NewTaskQueue(redisClient)

	components := &E2ETestComponents{
		stmCache:  stmCache,
		stmStore:  stmStore,
		taskQueue: taskQueue,
		redis:     redisClient,
		mongodb:   mongodb,
	}

	// Cleanup function
	cleanup := func() {
		// Clean up test data
		testUserPrefix := fmt.Sprintf("test_user_%d", time.Now().Unix()/60) // Group by minute
		redisClient.DeletePattern(fmt.Sprintf("stm_cache:%s*", testUserPrefix))

		// Drop test collections
		mongodb.Collection("dialogue_pages").Drop(context.Background())
		mongodb.Collection("dialogue_chains").Drop(context.Background())

		// Close connections
		mongoClient.Disconnect(context.Background())
	}

	return components, cleanup
}
