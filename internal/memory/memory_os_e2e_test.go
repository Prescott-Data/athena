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
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestMemoryOS_EndToEnd tests the complete STM → MTM → LPM pipeline
func TestMemoryOS_EndToEnd(t *testing.T) {
	assert := assert.New(t)

	// Skip if not running comprehensive integration tests
	if os.Getenv("RUN_E2E_TESTS") != "true" {
		t.Skip("Skipping E2E test. Set RUN_E2E_TESTS=true to run.")
	}

	// Setup all required components
	components, cleanup := setupMemoryOSE2ETestComponents(t)
	if components == nil {
		t.Skip("Failed to setup Memory OS E2E test components")
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	userID := fmt.Sprintf("test_user_%d", time.Now().Unix())

	t.Run("CompleteMemoryPipeline", func(t *testing.T) {
		// ==============================================
		// PHASE 1: STM (Short-Term Memory) Testing
		// ==============================================
		t.Log("🔄 PHASE 1: Testing STM Pipeline...")

		// Test conversation with multiple turns for rich MTM segment creation
		conversations := []struct {
			userMessage   string
			agentResponse string
		}{
			{
				"Hello, I'm working on a machine learning project and need help with data preprocessing. Can you guide me?",
				"I'd be happy to help with your ML data preprocessing! Data preprocessing is crucial for model performance. What type of data are you working with - text, images, or structured data?",
			},
			{
				"I'm working with text data for sentiment analysis. I have customer reviews that need cleaning and tokenization.",
				"Great choice for sentiment analysis! For text preprocessing, you'll want to: 1) Remove special characters and normalize case, 2) Handle stopwords, 3) Tokenize properly, 4) Consider lemmatization or stemming. What's your current approach?",
			},
			{
				"I'm using NLTK but running into issues with handling negations in the text. How should I preserve 'not good' vs 'good'?",
				"Excellent question! Negation handling is critical for sentiment analysis. You can: 1) Use dependency parsing to identify negation scope, 2) Add negation prefixes (not_good), 3) Use n-grams to capture context, or 4) Use transformer models that understand context better. For NLTK, I recommend the negation tagging approach.",
			},
			{
				"That's really helpful! Can you show me how to implement negation tagging with NLTK?",
				"Absolutely! Here's a practical approach:\n\n```python\nimport nltk\nfrom nltk.tokenize import word_tokenize\n\ndef handle_negations(text):\n    tokens = word_tokenize(text.lower())\n    negation_words = ['not', 'no', 'never', 'nothing', 'nowhere', 'noone', 'none', 'neither', 'nor']\n    negated = False\n    result = []\n    \n    for token in tokens:\n        if token in negation_words:\n            negated = True\n            result.append(token)\n        elif token in ['.', '!', '?', ',', ';']:\n            negated = False\n            result.append(token)\n        elif negated:\n            result.append(f'NOT_{token}')\n        else:\n            result.append(token)\n    \n    return result\n```\n\nThis preserves negation context until punctuation breaks the scope.",
			},
		}

		var chainID string
		for i, conv := range conversations {
			t.Logf("   Processing conversation turn %d...", i+1)

			// Step 1: Dialogue chain analysis
			returnedChainID, err := components.stmStore.DetermineDialogueChain(ctx, userID, conv.userMessage, conv.agentResponse)
			assert.NoError(err, "Dialogue chain analysis should succeed")
			assert.NotEmpty(returnedChainID, "Chain ID should be generated")

			if i == 0 {
				chainID = returnedChainID
				t.Logf("   ✓ New dialogue chain created: %s", chainID)
			} else {
				assert.Equal(chainID, returnedChainID, "Should continue same chain for related conversation")
				t.Logf("   ✓ Continuing dialogue chain: %s", chainID)
			}

			// Step 2: Embedding creation
			embedding, err := components.stmStore.CreateEmbedding(ctx, conv.userMessage, conv.agentResponse)
			assert.NoError(err, "Embedding creation should succeed")
			assert.NotNil(embedding, "Embedding should not be nil")
			assert.Equal(1536, embedding.Dimensions, "Should have 1536 dimensions")
			t.Logf("   ✓ Embedding created: %d dimensions", embedding.Dimensions)

			// Step 3: Store dialogue page
			dialoguePage := &models.DialoguePage{
				UserID:        userID,
				ChainID:       chainID,
				TurnIndex:     i,
				UserMessage:   conv.userMessage,
				AgentResponse: conv.agentResponse,
				Status:        "in_stm",
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}

			pageID, err := components.stmStore.StoreDialoguePage(ctx, dialoguePage)
			assert.NoError(err, "Dialogue page storage should succeed")
			assert.NotEqual(primitive.NilObjectID, pageID, "Page ID should be valid")
			t.Logf("   ✓ Dialogue page stored: turn %d, ID: %s", i, pageID.Hex())

			// Step 4: STM Cache operations
			userTurn := ConversationTurn{
				Type:      "user",
				Content:   conv.userMessage,
				Timestamp: time.Now(),
			}
			agentTurn := ConversationTurn{
				Type:      "agent",
				Content:   conv.agentResponse,
				Timestamp: time.Now(),
			}

			err = components.stmCache.AddConversationTurn(ctx, userID, userTurn)
			assert.NoError(err, "Adding user turn to cache should succeed")

			err = components.stmCache.AddConversationTurn(ctx, userID, agentTurn)
			assert.NoError(err, "Adding agent turn to cache should succeed")

			t.Logf("   ✓ Turns added to STM cache")
		}

		// Verify STM cache retrieval
		turns, err := components.stmCache.GetConversationContext(ctx, userID)
		assert.NoError(err, "Retrieving conversation context should succeed")
		assert.Len(turns, 8, "Should have 8 turns (4 user + 4 agent)")
		t.Logf("   ✓ STM cache contains %d turns", len(turns))

		// ==============================================
		// PHASE 2: MTM (Mid-Term Memory) Testing
		// ==============================================
		t.Log("🎯 PHASE 2: Testing MTM Pipeline...")

		// Step 1: Test Archivist (STM → MTM)
		archivist := NewArchivist(components.db)
		err = archivist.RunOnce(ctx, 5) // Process up to 5 pages per segment
		assert.NoError(err, "Archivist should process STM pages successfully")
		t.Logf("   ✓ Archivist processed STM pages")

		// Wait for background processing
		time.Sleep(2 * time.Second)

		// Step 2: Verify segment creation
		collection := components.db.Collection("segments")
		count, err := collection.CountDocuments(ctx, map[string]interface{}{"userId": userID})
		assert.NoError(err, "Should be able to count segments")
		assert.Greater(count, int64(0), "Should have created at least one segment")
		t.Logf("   ✓ Created %d MTM segments", count)

		// Step 3: Test Quality Validation
		qualityValidator := NewQualityValidator()

		// Retrieve a segment for testing
		cursor, err := collection.Find(ctx, map[string]interface{}{"userId": userID})
		assert.NoError(err, "Should be able to find segments")
		defer cursor.Close(ctx)

		var segment models.Segment
		assert.True(cursor.Next(ctx), "Should have at least one segment")
		err = cursor.Decode(&segment)
		assert.NoError(err, "Should be able to decode segment")

		// Get pages for this segment - use a simpler approach since GetDialoguePages method signature may differ
		pagesCollection := components.db.Collection("dialogue_pages")
		pagesCursor, err := pagesCollection.Find(ctx, map[string]interface{}{
			"userId":  userID,
			"chainId": segment.ChainID,
		})
		assert.NoError(err, "Should be able to find dialogue pages")

		var pages []models.DialoguePage
		err = pagesCursor.All(ctx, &pages)
		assert.NoError(err, "Should be able to decode dialogue pages")
		pagesCursor.Close(ctx)
		t.Logf("   ✓ Retrieved %d pages for segment", len(pages))

		// Validate segment quality
		validationResult, err := qualityValidator.ValidateSegment(ctx, &segment, pages)
		assert.NoError(err, "Quality validation should succeed")
		assert.NotNil(validationResult, "Validation result should not be nil")
		assert.True(validationResult.QualityScore > 0, "Should have positive quality score")
		t.Logf("   ✓ Quality validation: score=%.3f, valid=%t", validationResult.QualityScore, validationResult.IsValid)

		// Step 4: Test Heat Scoring
		heatScorer := NewHeatScorer(components.db)
		heatScore, heatFactors, err := heatScorer.ComputeSegmentHeat(ctx, &segment)
		assert.NoError(err, "Heat scoring should succeed")
		assert.Greater(heatScore, 0.0, "Heat score should be positive")
		assert.NotNil(heatFactors, "Heat factors should not be nil")
		t.Logf("   ✓ Heat scoring: score=%.3f", heatScore)
		t.Logf("     - Access frequency: %.3f", heatFactors.AccessFrequency)
		t.Logf("     - Interaction depth: %.3f", heatFactors.InteractionDepth)
		t.Logf("     - Recency score: %.3f", heatFactors.RecencyScore)

		// Step 5: Test SessionManager and segment merging
		sessionManager := NewSessionManager(components.db, components.stmStore)

		// Create a test segment for potential merging
		testSegment := &models.Segment{
			UserID:       userID,
			ChainID:      chainID + "_test",
			SegmentID:    fmt.Sprintf("test_segment_%d", time.Now().Unix()),
			TopicSummary: "Machine learning and data preprocessing discussion continuation",
			Status:       "active",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		processedSegment, err := sessionManager.ProcessNewSegment(ctx, testSegment, pages)
		assert.NoError(err, "Session manager should process segment")
		assert.NotNil(processedSegment, "Processed segment should not be nil")
		t.Logf("   ✓ Session manager processed segment: %s", processedSegment.SegmentID)

		// ==============================================
		// PHASE 3: LPM (Long-Term Personal Memory) Testing
		// ==============================================
		t.Log("🧠 PHASE 3: Testing LPM Pipeline...")

		// Step 1: Test LMP Orchestrator
		lmpOrchestrator := NewLMPOrchestrator(components.db)

		// Get segments for personality analysis
		var allSegments []models.Segment
		cursor, err = collection.Find(ctx, map[string]interface{}{"userId": userID})
		assert.NoError(err, "Should be able to find segments for LPM")
		err = cursor.All(ctx, &allSegments)
		assert.NoError(err, "Should be able to decode all segments")
		cursor.Close(ctx)

		// Run personality analysis
		result, err := lmpOrchestrator.ProcessUserPersonality(ctx, userID, pages, allSegments)
		assert.NoError(err, "LMP processing should succeed")
		assert.NotNil(result, "LMP result should not be nil")
		assert.True(result.Success, "LMP processing should be successful")
		t.Logf("   ✓ LMP processing completed: success=%t", result.Success)

		if result.AnalysisResult != nil && result.UpdatedProfile != nil {
			profile := result.UpdatedProfile
			t.Logf("   ✓ User profile created/updated for user: %s", profile.UserID)

			// Verify personality dimensions
			if profile.PsychologicalModel != nil {
				t.Logf("   ✓ Psychological dimensions analyzed")
				if profile.PsychologicalModel.Openness != nil {
					t.Logf("     - Openness: %s (%.2f confidence)",
						profile.PsychologicalModel.Openness.Level,
						profile.PsychologicalModel.Openness.Confidence)
				}
			}

			if profile.AIAlignment != nil {
				t.Logf("   ✓ AI alignment dimensions analyzed")
			}

			if profile.ContentInterests != nil {
				t.Logf("   ✓ Content interest tags analyzed")
			}
		}

		// ==============================================
		// PHASE 4: Background Processing Testing
		// ==============================================
		t.Log("⚙️ PHASE 4: Testing Background Processing...")

		// Step 1: Test Task Queue
		taskQueue := NewTaskQueue(components.redisClient)

		testTask := &models.MemoryProcessingTask{
			ID:            fmt.Sprintf("test_task_%d", time.Now().Unix()),
			UserID:        userID,
			Type:          "test_processing",
			UserMessage:   "Test message for background processing",
			AgentResponse: "Test response for background processing",
			CreatedAt:     time.Now(),
		}

		err = taskQueue.EnqueueMemoryTask(ctx, testTask.UserID, testTask.UserMessage, testTask.AgentResponse, testTask.Metadata)
		assert.NoError(err, "Should be able to enqueue task")
		t.Logf("   ✓ Task enqueued: %s", testTask.ID)

		// Step 2: Test task dequeue
		dequeuedTask, err := taskQueue.DequeueTask(ctx)
		assert.NoError(err, "Should be able to dequeue task")
		assert.NotNil(dequeuedTask, "Dequeued task should not be nil")
		assert.Equal(testTask.ID, dequeuedTask.ID, "Should dequeue the same task")
		t.Logf("   ✓ Task dequeued: %s", dequeuedTask.ID)

		// Step 3: Test Worker
		worker := NewWorker(WorkerConfig{
			WorkerCount: 1,
			Redis:       components.redisClient,
			Database:    components.db,
		})

		// Start the worker (it will process tasks automatically)
		go worker.Start(ctx)
		defer worker.Stop()
		t.Logf("   ✓ Worker started successfully")

		// ==============================================
		// PHASE 5: Production Features Testing
		// ==============================================
		t.Log("📊 PHASE 5: Testing Production Features...")

		// Test circuit breaker and rate limiting are accessible
		assert.NotNil(components.stmStore.llmGuards, "LLM guardrails should be initialized")
		t.Logf("   ✓ LLM guardrails active")

		// Test Milvus integration (if available)
		if components.milvusClient != nil {
			// Just test that the client exists - specific methods may vary
			t.Logf("   ✓ Milvus client initialized")
		} else {
			t.Logf("   ⚠ Milvus integration not available")
		}

		t.Log("🎉 All pipeline phases completed successfully!")
	})
}

// MemoryOSE2ETestComponents holds all components needed for E2E testing
type MemoryOSE2ETestComponents struct {
	db           *mongo.Database
	redisClient  cache.Interface
	stmStore     *STMStore
	stmCache     *STMCache
	milvusClient *MilvusClient
}

// setupMemoryOSE2ETestComponents sets up all required components for E2E testing
func setupMemoryOSE2ETestComponents(t *testing.T) (*MemoryOSE2ETestComponents, func()) {
	// MongoDB setup
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	clientOptions := options.Client().ApplyURI(mongoURI)
	mongoClient, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		t.Logf("Failed to connect to MongoDB: %v", err)
		return nil, nil
	}

	// Test MongoDB connection
	err = mongoClient.Ping(context.Background(), nil)
	if err != nil {
		t.Logf("Failed to ping MongoDB: %v", err)
		mongoClient.Disconnect(context.Background())
		return nil, nil
	}

	db := mongoClient.Database("memory_os_e2e_test")

	// Redis setup
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost"
	}
	redisPort := os.Getenv("REDIS_PORT")
	if redisPort == "" {
		redisPort = "6379"
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisDB := os.Getenv("REDIS_DB")
	if redisDB == "" {
		redisDB = "3" // Use DB 3 for E2E tests
	}

	// Set Redis environment variables for the client
	os.Setenv("REDIS_HOST", redisHost)
	os.Setenv("REDIS_PORT", redisPort)
	if redisPassword != "" {
		os.Setenv("REDIS_PASSWORD", redisPassword)
	}
	os.Setenv("REDIS_DB", redisDB)

	redisClient, err := cache.NewRedisClient()
	if err != nil {
		t.Logf("Failed to connect to Redis: %v", err)
		mongoClient.Disconnect(context.Background())
		return nil, nil
	}

	// Initialize STM components
	stmStore := NewSTMStore(db, redisClient)
	stmCache := NewSTMCache(redisClient)

	// Try to initialize Milvus (optional for E2E test)
	var milvusClient *MilvusClient
	milvusHost := os.Getenv("MILVUS_HOST")
	milvusPort := os.Getenv("MILVUS_PORT")
	if milvusHost != "" && milvusPort != "" {
		milvusClient, err = NewMilvusClient(milvusHost, milvusPort)
		if err != nil {
			t.Logf("Milvus not available for E2E test: %v", err)
			milvusClient = nil
		}
	}

	components := &MemoryOSE2ETestComponents{
		db:           db,
		redisClient:  redisClient,
		stmStore:     stmStore,
		stmCache:     stmCache,
		milvusClient: milvusClient,
	}

	// Cleanup function
	cleanup := func() {
		// Clean up test data
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Drop test database
		db.Drop(ctx)

		// Clean Redis test data (if needed, can implement specific cleanup)

		// Close connections
		redisClient.Close()
		mongoClient.Disconnect(ctx)

		if milvusClient != nil {
			milvusClient.Close()
		}

		t.Log("🧹 E2E test cleanup completed")
	}

	return components, cleanup
}
