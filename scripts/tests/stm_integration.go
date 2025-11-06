package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	azureVMIP     = "172.190.152.215"
	redisPassword = "dromos_redis_2024"
	mongoURI      = "mongodb://memory_user:memory_password_2024@172.190.152.215:27017/memory_os?retryWrites=true&authSource=memory_os"
	testUserID    = "test_stm_user_123"
)

// STMEvent represents an event for STM testing
type STMEvent struct {
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// DialoguePage represents a dialogue page for MongoDB testing
type DialoguePage struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	UserID        string             `bson:"userId"`
	ChainID       string             `bson:"chainId"`
	UserMessage   string             `bson:"userMessage"`
	AgentResponse string             `bson:"agentResponse"`
	Status        string             `bson:"status"`
	CreatedAt     time.Time          `bson:"createdAt"`
	UpdatedAt     time.Time          `bson:"updatedAt"`
}

func main() {
	fmt.Println("🧠 Testing STM (Short-Term Memory) with Azure Infrastructure")
	fmt.Println("============================================================")

	ctx := context.Background()

	// Test 1: STM Cache (Redis) Operations
	fmt.Println("\n1. 🔴 Testing STM Cache (Redis) Operations...")
	if testSTMCache(ctx) {
		fmt.Println("   ✅ STM Cache: SUCCESS")
	} else {
		fmt.Println("   ❌ STM Cache: FAILED")
		return
	}

	// Test 2: STM Store (MongoDB) Operations
	fmt.Println("\n2. 🍃 Testing STM Store (MongoDB) Operations...")
	if testSTMStore(ctx) {
		fmt.Println("   ✅ STM Store: SUCCESS")
	} else {
		fmt.Println("   ❌ STM Store: FAILED")
		return
	}

	// Test 3: STM Integration (Cache + Store)
	fmt.Println("\n3. 🔄 Testing STM Integration (Cache + Store)...")
	if testSTMIntegration(ctx) {
		fmt.Println("   ✅ STM Integration: SUCCESS")
	} else {
		fmt.Println("   ❌ STM Integration: FAILED")
		return
	}

	// Test 4: STM Performance Testing
	fmt.Println("\n4. ⚡ Testing STM Performance...")
	if testSTMPerformance(ctx) {
		fmt.Println("   ✅ STM Performance: SUCCESS")
	} else {
		fmt.Println("   ❌ STM Performance: FAILED")
		return
	}

	fmt.Println("\n=============================================================")
	fmt.Println("🎉 ALL STM TESTS PASSED!")
	fmt.Println("✅ Short-Term Memory is working correctly with Azure infrastructure")
}

func testSTMCache(ctx context.Context) bool {
	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:6379", azureVMIP),
		Password: redisPassword,
		DB:       0,
	})
	defer rdb.Close()

	// Test Redis connectivity
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Redis connection failed: %v\n", err)
		return false
	}

	cacheKey := fmt.Sprintf("stm_cache:user_%s", testUserID)

	// Test 1: Store events
	events := []STMEvent{
		{Type: "user", Content: "Hello, I need help with machine learning", Timestamp: time.Now().Add(-5 * time.Minute)},
		{Type: "agent", Content: "I'd be happy to help you with machine learning! What specific area would you like to explore?", Timestamp: time.Now().Add(-4 * time.Minute)},
		{Type: "user", Content: "I want to learn about neural networks", Timestamp: time.Now().Add(-3 * time.Minute)},
		{Type: "agent", Content: "Neural networks are a fascinating topic! Let me explain the basics of how they work.", Timestamp: time.Now().Add(-2 * time.Minute)},
	}

	// Clear any existing cache
	rdb.Del(ctx, cacheKey)

	// Add events to cache (LPUSH for newest first)
	for i := len(events) - 1; i >= 0; i-- {
		eventJSON, err := json.Marshal(events[i])
		if err != nil {
			fmt.Printf("   ⚠️  Failed to marshal event: %v\n", err)
			return false
		}

		err = rdb.LPush(ctx, cacheKey, string(eventJSON)).Err()
		if err != nil {
			fmt.Printf("   ⚠️  Failed to LPUSH event: %v\n", err)
			return false
		}
	}

	// Set TTL (2 hours)
	err = rdb.Expire(ctx, cacheKey, 2*time.Hour).Err()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to set TTL: %v\n", err)
		return false
	}

	// Test 2: Retrieve context
	retrievedEvents, err := rdb.LRange(ctx, cacheKey, 0, 9).Result() // Get last 10 events
	if err != nil {
		fmt.Printf("   ⚠️  Failed to retrieve events: %v\n", err)
		return false
	}

	if len(retrievedEvents) != len(events) {
		fmt.Printf("   ⚠️  Expected %d events, got %d\n", len(events), len(retrievedEvents))
		return false
	}

	// Test 3: Verify event order and content
	for i, eventStr := range retrievedEvents {
		var event STMEvent
		err := json.Unmarshal([]byte(eventStr), &event)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to unmarshal event %d: %v\n", i, err)
			return false
		}

		expectedEvent := events[i]
		if event.Type != expectedEvent.Type || event.Content != expectedEvent.Content {
			fmt.Printf("   ⚠️  Event %d mismatch: expected %s, got %s\n", i, expectedEvent.Content, event.Content)
			return false
		}
	}

	// Test 4: Sliding window behavior (LTRIM)
	// Add more events to test max limit
	for j := 0; j < 8; j++ {
		newEvent := STMEvent{
			Type:      "user",
			Content:   fmt.Sprintf("Additional message %d", j+1),
			Timestamp: time.Now(),
		}
		eventJSON, _ := json.Marshal(newEvent)
		rdb.LPush(ctx, cacheKey, string(eventJSON))
		rdb.LTrim(ctx, cacheKey, 0, 9) // Keep only 10 events max
	}

	finalLength, err := rdb.LLen(ctx, cacheKey).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to get final length: %v\n", err)
		return false
	}

	if finalLength > 10 {
		fmt.Printf("   ⚠️  Cache exceeded max length: %d > 10\n", finalLength)
		return false
	}

	// Cleanup
	rdb.Del(ctx, cacheKey)

	fmt.Printf("   📊 STM Cache operations: %d events stored, sliding window working\n", len(events))
	return true
}

func testSTMStore(ctx context.Context) bool {
	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("dialogue_pages")

	// Test 1: Store dialogue pages
	testPages := []DialoguePage{
		{
			UserID:        testUserID,
			ChainID:       "chain_test_123",
			UserMessage:   "Hello, I need help with machine learning",
			AgentResponse: "I'd be happy to help you with machine learning! What specific area would you like to explore?",
			Status:        "in_stm",
			CreatedAt:     time.Now().Add(-10 * time.Minute),
			UpdatedAt:     time.Now().Add(-10 * time.Minute),
		},
		{
			UserID:        testUserID,
			ChainID:       "chain_test_123",
			UserMessage:   "I want to learn about neural networks",
			AgentResponse: "Neural networks are a fascinating topic! Let me explain the basics of how they work.",
			Status:        "in_stm",
			CreatedAt:     time.Now().Add(-5 * time.Minute),
			UpdatedAt:     time.Now().Add(-5 * time.Minute),
		},
	}

	// Insert test pages
	var insertedIDs []primitive.ObjectID
	for _, page := range testPages {
		result, err := collection.InsertOne(ctx, page)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to insert dialogue page: %v\n", err)
			return false
		}
		insertedIDs = append(insertedIDs, result.InsertedID.(primitive.ObjectID))
	}

	// Test 2: Retrieve recent conversation context
	filter := bson.M{
		"userId": testUserID,
		"status": "in_stm",
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(10)

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find recent pages: %v\n", err)
		return false
	}
	defer cursor.Close(ctx)

	var retrievedPages []DialoguePage
	if err := cursor.All(ctx, &retrievedPages); err != nil {
		fmt.Printf("   ⚠️  Failed to decode pages: %v\n", err)
		return false
	}

	if len(retrievedPages) != len(testPages) {
		fmt.Printf("   ⚠️  Expected %d pages, got %d\n", len(testPages), len(retrievedPages))
		return false
	}

	// Test 3: Verify page order (most recent first)
	if retrievedPages[0].CreatedAt.Before(retrievedPages[1].CreatedAt) {
		fmt.Printf("   ⚠️  Pages not in correct order\n")
		return false
	}

	// Test 4: Chain-based retrieval
	chainFilter := bson.M{"chainId": "chain_test_123"}
	chainOpts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}) // Chronological for chains

	chainCursor, err := collection.Find(ctx, chainFilter, chainOpts)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find chain pages: %v\n", err)
		return false
	}
	defer chainCursor.Close(ctx)

	var chainPages []DialoguePage
	if err := chainCursor.All(ctx, &chainPages); err != nil {
		fmt.Printf("   ⚠️  Failed to decode chain pages: %v\n", err)
		return false
	}

	if len(chainPages) != 2 {
		fmt.Printf("   ⚠️  Expected 2 chain pages, got %d\n", len(chainPages))
		return false
	}

	// Cleanup
	_, err = collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": insertedIDs}})
	if err != nil {
		fmt.Printf("   ⚠️  Failed to cleanup test data: %v\n", err)
		return false
	}

	fmt.Printf("   📊 STM Store operations: %d pages stored and retrieved\n", len(testPages))
	return true
}

func testSTMIntegration(ctx context.Context) bool {
	// Test integration between Redis cache and MongoDB store
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:6379", azureVMIP),
		Password: redisPassword,
		DB:       0,
	})
	defer rdb.Close()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("dialogue_pages")

	cacheKey := fmt.Sprintf("stm_cache:user_%s", testUserID)

	// Test scenario: User has active conversation
	// 1. Store in MongoDB (persistent)
	dialoguePage := DialoguePage{
		UserID:        testUserID,
		ChainID:       "chain_integration_test",
		UserMessage:   "Can you explain transformer architectures?",
		AgentResponse: "Transformers are a type of neural network architecture that has revolutionized natural language processing...",
		Status:        "in_stm",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	result, err := collection.InsertOne(ctx, dialoguePage)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to store dialogue page: %v\n", err)
		return false
	}
	insertedID := result.InsertedID.(primitive.ObjectID)

	// 2. Cache the event in Redis (fast access)
	event := STMEvent{
		Type:      "user",
		Content:   dialoguePage.UserMessage,
		Timestamp: dialoguePage.CreatedAt,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to marshal event: %v\n", err)
		return false
	}

	err = rdb.LPush(ctx, cacheKey, string(eventJSON)).Err()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to cache event: %v\n", err)
		return false
	}

	// Add agent response to cache
	agentEvent := STMEvent{
		Type:      "agent",
		Content:   dialoguePage.AgentResponse,
		Timestamp: dialoguePage.CreatedAt.Add(time.Second),
	}

	agentEventJSON, _ := json.Marshal(agentEvent)
	rdb.LPush(ctx, cacheKey, string(agentEventJSON))

	// 3. Test cache-first retrieval pattern
	// First check cache (fast)
	cachedEvents, err := rdb.LRange(ctx, cacheKey, 0, 9).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to retrieve from cache: %v\n", err)
		return false
	}

	if len(cachedEvents) != 2 {
		fmt.Printf("   ⚠️  Expected 2 cached events, got %d\n", len(cachedEvents))
		return false
	}

	// 4. Test fallback to MongoDB if cache miss
	rdb.Del(ctx, cacheKey) // Simulate cache miss

	// Retrieve from MongoDB
	filter := bson.M{"userId": testUserID}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(10)

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to fallback to MongoDB: %v\n", err)
		return false
	}
	defer cursor.Close(ctx)

	var fallbackPages []DialoguePage
	if err := cursor.All(ctx, &fallbackPages); err != nil {
		fmt.Printf("   ⚠️  Failed to decode fallback pages: %v\n", err)
		return false
	}

	if len(fallbackPages) == 0 {
		fmt.Printf("   ⚠️  No pages found in MongoDB fallback\n")
		return false
	}

	// 5. Rebuild cache from MongoDB data
	for i := len(fallbackPages) - 1; i >= 0; i-- {
		page := fallbackPages[i]
		
		// Add user event
		userEvent := STMEvent{
			Type:      "user",
			Content:   page.UserMessage,
			Timestamp: page.CreatedAt,
		}
		userJSON, _ := json.Marshal(userEvent)
		rdb.LPush(ctx, cacheKey, string(userJSON))

		// Add agent event
		agentEvent := STMEvent{
			Type:      "agent",
			Content:   page.AgentResponse,
			Timestamp: page.CreatedAt.Add(time.Second),
		}
		agentJSON, _ := json.Marshal(agentEvent)
		rdb.LPush(ctx, cacheKey, string(agentJSON))
	}

	// Verify cache rebuild
	rebuiltEvents, err := rdb.LRange(ctx, cacheKey, 0, -1).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to verify cache rebuild: %v\n", err)
		return false
	}

	if len(rebuiltEvents) != 2 {
		fmt.Printf("   ⚠️  Cache rebuild failed: expected 2 events, got %d\n", len(rebuiltEvents))
		return false
	}

	// Cleanup
	collection.DeleteOne(ctx, bson.M{"_id": insertedID})
	rdb.Del(ctx, cacheKey)

	fmt.Printf("   📊 STM Integration: Cache-first pattern working, MongoDB fallback successful\n")
	return true
}

func testSTMPerformance(ctx context.Context) bool {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:6379", azureVMIP),
		Password: redisPassword,
		DB:       0,
	})
	defer rdb.Close()

	// Test Redis performance with multiple operations
	cacheKey := fmt.Sprintf("stm_perf_test:user_%s", testUserID)
	
	// Test 1: Batch operations performance
	start := time.Now()
	
	// Simulate 50 conversation events
	for i := 0; i < 50; i++ {
		event := STMEvent{
			Type:      "user",
			Content:   fmt.Sprintf("Test message %d for performance testing", i+1),
			Timestamp: time.Now(),
		}
		
		eventJSON, _ := json.Marshal(event)
		err := rdb.LPush(ctx, cacheKey, string(eventJSON)).Err()
		if err != nil {
			fmt.Printf("   ⚠️  Failed to push event %d: %v\n", i+1, err)
			return false
		}
		
		// Maintain sliding window
		rdb.LTrim(ctx, cacheKey, 0, 9)
	}
	
	batchDuration := time.Since(start)
	
	// Test 2: Retrieval performance
	start = time.Now()
	
	events, err := rdb.LRange(ctx, cacheKey, 0, 9).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Failed to retrieve events: %v\n", err)
		return false
	}
	
	retrievalDuration := time.Since(start)
	
	// Test 3: Network latency check
	start = time.Now()
	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Ping failed: %v\n", err)
		return false
	}
	pingDuration := time.Since(start)
	
	// Performance thresholds
	if batchDuration > 500*time.Millisecond {
		fmt.Printf("   ⚠️  Batch operations too slow: %v > 500ms\n", batchDuration)
		return false
	}
	
	if retrievalDuration > 50*time.Millisecond {
		fmt.Printf("   ⚠️  Retrieval too slow: %v > 50ms\n", retrievalDuration)
		return false
	}
	
	if pingDuration > 100*time.Millisecond {
		fmt.Printf("   ⚠️  Network latency too high: %v > 100ms\n", pingDuration)
		return false
	}

	// Verify final state
	if len(events) != 10 {
		fmt.Printf("   ⚠️  Expected 10 events after batch, got %d\n", len(events))
		return false
	}

	// Cleanup
	rdb.Del(ctx, cacheKey)

	fmt.Printf("   📊 STM Performance: Batch: %v, Retrieval: %v, Ping: %v\n", 
		batchDuration, retrievalDuration, pingDuration)
	return true
}
