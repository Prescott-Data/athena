package memory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"

	_ "github.com/joho/godotenv/autoload"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// setupRedisTestClient creates a new Redis client for testing.
func setupRedisTestClient() (cache.Interface, error) {
	// Set environment variables for testing if not already set
	if os.Getenv("REDIS_HOST") == "" {
		os.Setenv("REDIS_HOST", "172.190.152.215")
	}
	if os.Getenv("REDIS_PORT") == "" {
		os.Setenv("REDIS_PORT", "6379")
	}
	if os.Getenv("REDIS_PASSWORD") == "" {
		os.Setenv("REDIS_PASSWORD", "dromos_redis_2024")
	}
	if os.Getenv("REDIS_DB") == "" {
		os.Setenv("REDIS_DB", "3") // Use DB 3 for testing (matches E2E test)
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

// TestSTMCache_Integration validates STM cache operations with a real Redis.
func TestSTMCache_Integration(t *testing.T) {
	assert := assert.New(t)

	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer redisClient.Close()

	stmCache := NewSTMCache(redisClient)
	ctx := context.Background()
	userID := "integration_test_user_1"

	// Clear any existing data for this user key
	_ = stmCache.ClearConversationContext(ctx, userID)

	// Test 1: Add conversation turns
	turn1 := ConversationTurn{
		Type:      "user",
		Content:   "Hello, how are you?",
		Timestamp: time.Now().Add(-2 * time.Minute),
	}
	turn2 := ConversationTurn{
		Type:      "agent",
		Content:   "I'm doing well, thank you!",
		Timestamp: time.Now().Add(-1 * time.Minute),
	}

	err = stmCache.AddConversationTurn(ctx, userID, turn1)
	assert.NoError(err)

	err = stmCache.AddConversationTurn(ctx, userID, turn2)
	assert.NoError(err)

	// Test 2: Retrieve conversation context
	turns, err := stmCache.GetConversationContext(ctx, userID)
	assert.NoError(err)
	assert.Len(turns, 2)

	// Verify turns are in reverse order (newest first)
	assert.Equal("agent", turns[0].Type)
	assert.Equal("I'm doing well, thank you!", turns[0].Content)
	assert.Equal("user", turns[1].Type)
	assert.Equal("Hello, how are you?", turns[1].Content)

	// Test 3: Verify max turns limit works
	originalMaxTurns := StmCacheMaxTurns
	StmCacheMaxTurns = 2                                   // Temporarily set to 2
	defer func() { StmCacheMaxTurns = originalMaxTurns }() // Reset after test

	// Clear and add 3 turns
	_ = stmCache.ClearConversationContext(ctx, userID)

	for i := 1; i <= 3; i++ {
		turn := ConversationTurn{
			Type:      "user",
			Content:   fmt.Sprintf("Message %d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		err = stmCache.AddConversationTurn(ctx, userID, turn)
		assert.NoError(err)
	}

	// Should only return 2 most recent turns
	turns, err = stmCache.GetConversationContext(ctx, userID)
	assert.NoError(err)
	assert.Len(turns, 2)
	assert.Equal("Message 3", turns[0].Content) // Newest first
	assert.Equal("Message 2", turns[1].Content)
}

// TestSTMCache_Metrics_Integration verifies that metrics are recorded
func TestSTMCache_Metrics_Integration(t *testing.T) {
	assert := assert.New(t)

	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping metrics integration test: %v", err)
	}
	defer redisClient.Close()

	stmCache := NewSTMCache(redisClient)
	ctx := context.Background()
	userID := "metrics_test_user_1"

	// Clear any existing data for this user key
	_ = stmCache.ClearConversationContext(ctx, userID)

	// Get initial metric counts
	initialGetOK := testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "ok"))
	initialGetError := testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "error"))
	initialAppendOK := testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "ok"))
	initialAppendError := testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "error"))

	// 1. Test AddConversationTurn (should increment append_ok)
	turn1 := ConversationTurn{Type: "user", Content: "Hi", Timestamp: time.Now()}
	err = stmCache.AddConversationTurn(ctx, userID, turn1)
	assert.NoError(err)
	assert.Equal(t, initialAppendOK+1, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "ok")))
	assert.Equal(t, initialAppendError, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "error")))

	// 2. Test GetConversationContext (should increment get_ok)
	turns, err := stmCache.GetConversationContext(ctx, userID)
	assert.NoError(err)
	assert.Equal(t, 1, len(turns))
	assert.Equal(t, initialGetOK+1, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "ok")))
	assert.Equal(t, initialGetError, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "error")))

	// 3. Simulate an error scenario for AddConversationTurn (e.g., connection issue)
	//    This is tricky with a real Redis client as it's hard to induce an error directly.
	//    For now, we rely on the unit tests' mock for explicit error path testing.

	// 4. Simulate an error scenario for GetConversationContext (e.g., connection issue)
	//    Similar to above, hard to induce. The current implementation logs warnings but doesn't return errors
	//    for cache misses, so 'get' errors are less frequent. We check 'error' metric anyway.
	assert.Equal(t, initialAppendError, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "error")))
	assert.Equal(t, initialGetError, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "error")))

	// Ensure other metrics remain unchanged
	assert.Equal(t, initialGetOK+1, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("get", "ok")))
	assert.Equal(t, initialAppendOK+1, testutil.ToFloat64(MetricSTMCacheOps.WithLabelValues("append", "ok")))

	// Test max turns trimming
	StmCacheMaxTurns = 2                     // Temporarily set to 2
	defer func() { StmCacheMaxTurns = 10 }() // Reset after test

	_ = stmCache.ClearConversationContext(ctx, userID)

	// Add 3 turns, only 2 should remain
	_ = stmCache.AddConversationTurn(ctx, userID, ConversationTurn{Type: "u", Content: "1", Timestamp: time.Now()})
	_ = stmCache.AddConversationTurn(ctx, userID, ConversationTurn{Type: "u", Content: "2", Timestamp: time.Now()})
	_ = stmCache.AddConversationTurn(ctx, userID, ConversationTurn{Type: "u", Content: "3", Timestamp: time.Now()})

	turns, err = stmCache.GetConversationContext(ctx, userID)
	assert.NoError(err)
	assert.Len(turns, 2)
	assert.Equal(t, "3", turns[0].Content) // Newest first
	assert.Equal(t, "2", turns[1].Content)
}
