package memory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dromos-org/memory-os/api/middleware"
	"github.com/dromos-org/memory-os/internal/cache"
	"github.com/dromos-org/memory-os/internal/database"
	"github.com/dromos-org/memory-os/internal/models"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// TestMemoryOS_EndToEnd tests the complete STM → MTM → LPM pipeline
// TestTenantIsolation verifies that tenant data is properly isolated
func TestTenantIsolation(t *testing.T) {
	assert := assert.New(t)

	// Setup test components
	components, cleanup := setupMemoryOSE2ETestComponents(t)
	defer cleanup()

	ctx := context.Background()

	// Test data for multiple tenants
	tenants := []struct {
		tenantID string
		userID   string
		agentID  string
		message  string
	}{
		{"tenant_a", "user_1", "agent_chat", "Hello from tenant A"},
		{"tenant_a", "user_2", "agent_chat", "Hello from tenant A user 2"},
		{"tenant_b", "user_1", "agent_chat", "Hello from tenant B"},
		{"tenant_b", "user_1", "agent_search", "Search query from tenant B"},
	}

	// Store interactions for each tenant/user/agent combination
	for i, testCase := range tenants {
		t.Logf("   Storing interaction %d: tenant=%s, user=%s, agent=%s",
			i+1, testCase.tenantID, testCase.userID, testCase.agentID)

		// Create dialogue page with tenant scoping
		page := &models.DialoguePage{
			TenantID:      testCase.tenantID,
			UserID:        testCase.userID,
			AgentID:       testCase.agentID,
			ChainID:       fmt.Sprintf("chain_%s_%s_%s", testCase.tenantID, testCase.userID, testCase.agentID),
			TurnIndex:     0,
			UserMessage:   testCase.message,
			AgentResponse: fmt.Sprintf("Response to: %s", testCase.message),
			Status:        "in_stm",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		// Store the page
		pageID, err := components.stmStore.StoreDialoguePage(ctx, page)
		assert.NoError(err, "Should store dialogue page for tenant %s", testCase.tenantID)
		assert.NotEqual(primitive.NilObjectID, pageID, "Should return valid page ID")

		// Verify the stored page has correct tenant data
		storedPage, err := components.stmStore.getLastDialoguePage(ctx, testCase.tenantID, testCase.userID, testCase.agentID)
		assert.NoError(err, "Should retrieve stored page")
		assert.NotNil(storedPage, "Should find stored page")
		assert.Equal(testCase.tenantID, storedPage.TenantID, "Tenant ID should match authenticated context")
		assert.Equal(testCase.userID, storedPage.UserID, "User ID should match authenticated context")
		assert.Equal(testCase.agentID, storedPage.AgentID, "Agent ID should match authenticated context")

		// Verify that the data is properly isolated by checking that other tenant's data is not accessible
		// Try to access data from a different tenant - this should return nil or error
		wrongTenantPage, err := components.stmStore.getLastDialoguePage(ctx, "wrong_tenant", testCase.userID, testCase.agentID)
		assert.NoError(err, "Querying wrong tenant should not error")
		if wrongTenantPage != nil {
			assert.NotEqual(testCase.tenantID, wrongTenantPage.TenantID, "Should not return data from wrong tenant")
		}
	}

	// Test tenant isolation - verify each tenant can only see their own data
	for _, tenant := range []string{"tenant_a", "tenant_b"} {
		t.Logf("   Testing tenant isolation for: %s", tenant)

		// Count pages for this tenant (this would require a new method to query by tenant)
		// For now, just verify we can retrieve specific tenant data

		// Test tenant_a user_1 agent_chat
		pageA1, err := components.stmStore.getLastDialoguePage(ctx, tenant, "user_1", "agent_chat")
		assert.NoError(err, "Should query tenant %s data", tenant)
		assert.NotNil(pageA1, "Should find data for tenant %s", tenant)
		assert.Equal(tenant, pageA1.TenantID, "Should only return correct tenant data")
	}

	t.Log("✅ Tenant isolation test completed successfully")
}

// TestAuthValidation tests the tenant/user validation functions
func TestAuthValidation(t *testing.T) {
	assert := assert.New(t)

	t.Run("ValidateTenantUserMatch_Success", func(t *testing.T) {
		ctx := context.Background()

		// Set up context with authenticated values
		ctx = context.WithValue(ctx, middleware.CtxTenantID, "tenant_a")
		ctx = context.WithValue(ctx, middleware.CtxUserID, "user_1")

		// Test successful validation
		err := middleware.ValidateTenantUserMatch(ctx, "tenant_a", "user_1")
		assert.NoError(err, "Should allow matching tenant/user")

		// Test empty client values (should pass)
		err = middleware.ValidateTenantUserMatch(ctx, "", "")
		assert.NoError(err, "Should allow empty client values")
	})

	t.Run("ValidateTenantUserMatch_Failures", func(t *testing.T) {
		ctx := context.Background()

		// Set up context with authenticated values
		ctx = context.WithValue(ctx, middleware.CtxTenantID, "tenant_a")
		ctx = context.WithValue(ctx, middleware.CtxUserID, "user_1")

		// Test mismatched tenant
		err := middleware.ValidateTenantUserMatch(ctx, "tenant_b", "user_1")
		assert.Error(err, "Should reject mismatched tenant")
		assert.Contains(err.Error(), "tenant_id", "Error should mention tenant_id")

		// Test mismatched user
		err = middleware.ValidateTenantUserMatch(ctx, "tenant_a", "user_2")
		assert.Error(err, "Should reject mismatched user")
		assert.Contains(err.Error(), "user_id", "Error should mention user_id")
	})

	t.Run("ContextUtilities", func(t *testing.T) {
		ctx := context.Background()

		// Test setting values in context (using middleware context keys)
		ctx = context.WithValue(ctx, middleware.CtxTenantID, "test_tenant")
		ctx = context.WithValue(ctx, middleware.CtxUserID, "test_user")
		ctx = context.WithValue(ctx, middleware.CtxAgentID, "test_agent")

		// Test extracting values
		tenantID, err := middleware.TenantIDFromContext(ctx)
		assert.NoError(err, "Should extract tenant ID")
		assert.Equal("test_tenant", tenantID, "Should return correct tenant ID")

		userID, err := middleware.UserIDFromContext(ctx)
		assert.NoError(err, "Should extract user ID")
		assert.Equal("test_user", userID, "Should return correct user ID")

		agentID, err := middleware.AgentIDFromContext(ctx)
		assert.NoError(err, "Should extract agent ID")
		assert.Equal("test_agent", agentID, "Should return correct agent ID")

		// Test missing values
		emptyCtx := context.Background()
		_, err = middleware.TenantIDFromContext(emptyCtx)
		assert.Error(err, "Should error on missing tenant ID")
		assert.Contains(err.Error(), "tenant_id not found", "Error should mention missing tenant_id")
	})
}

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
			// Test with authenticated tenant/user/agent context
			returnedChainID, err := components.stmStore.DetermineDialogueChain(ctx, "test_tenant", userID, "test_agent", conv.userMessage, conv.agentResponse)
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
	mongoClient  *mongo.Client // Keep client reference for cleanup
	redisClient  cache.Interface
	stmStore     *STMStore
	stmCache     *STMCache
	milvusClient *MilvusClient
}

// setupMemoryOSE2ETestComponents sets up all required components for E2E testing
func setupMemoryOSE2ETestComponents(t *testing.T) (*MemoryOSE2ETestComponents, func()) {
	// MongoDB setup - use the proper database connection method
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://memory_user:memory_password_2024@172.190.152.215:27017/memory_os?authSource=memory_os"
	}

	mongoDB := os.Getenv("MONGO_DB")
	if mongoDB == "" {
		mongoDB = "memory_os"
	}

	// Use the same connection method as the production code
	config := database.ConnectionConfig{
		URI:            mongoURI,
		DatabaseName:   mongoDB,
		ConnectTimeout: 10 * time.Second,
	}

	mongoClient, db, err := database.ConnectMongoDB(config)
	if err != nil {
		t.Logf("Failed to connect to MongoDB: %v", err)
		return nil, nil
	}

	// Redis setup
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "172.190.152.215" // Azure Redis server
	}
	redisPort := os.Getenv("REDIS_PORT")
	if redisPort == "" {
		redisPort = "6379"
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	if redisPassword == "" {
		redisPassword = "dromos_redis_2024"
	}
	redisDB := os.Getenv("REDIS_DB")
	if redisDB == "" {
		redisDB = "3" // Use DB 3 for E2E tests
	}
	redisPoolSize := os.Getenv("REDIS_POOL_SIZE")
	if redisPoolSize == "" {
		redisPoolSize = "10"
	}
	redisPoolTimeout := os.Getenv("REDIS_POOL_TIMEOUT")
	if redisPoolTimeout == "" {
		redisPoolTimeout = "30"
	}
	cacheTTL := os.Getenv("CACHE_TTL")
	if cacheTTL == "" {
		cacheTTL = "3600"
	}

	// Set Redis environment variables for the client
	os.Setenv("REDIS_HOST", redisHost)
	os.Setenv("REDIS_PORT", redisPort)
	os.Setenv("REDIS_PASSWORD", redisPassword)
	os.Setenv("REDIS_DB", redisDB)
	os.Setenv("REDIS_POOL_SIZE", redisPoolSize)
	os.Setenv("REDIS_POOL_TIMEOUT", redisPoolTimeout)
	os.Setenv("CACHE_TTL", cacheTTL)

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
		mongoClient:  mongoClient,
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

		// Drop test database (only if using test database)
		if mongoDB == "memory_os" {
			t.Log("⚠️  Skipping database drop for production database 'memory_os'")
		} else {
			db.Drop(ctx)
		}

		// Clean Redis test data (if needed, can implement specific cleanup)

		// Close connections
		redisClient.Close()
		if mongoClient != nil {
			mongoClient.Disconnect(ctx)
		}

		if milvusClient != nil {
			milvusClient.Close()
		}

		t.Log("🧹 E2E test cleanup completed")
	}

	return components, cleanup
}
