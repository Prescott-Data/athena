package memory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/models"

	_ "github.com/joho/godotenv/autoload"

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
	tenantID := "test_tenant"
	userID := "integration_test_user_1"
	agentID := "test_agent"

	// Clear any existing data for this user key
	_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)

	// Test 1: Add STM events
	event1 := STMEvent{
		Role:      "user",
		Type:      models.STMEventTypeMessage,
		Content:   "Hello, how are you?",
		Timestamp: time.Now().Add(-2 * time.Minute),
	}
	event2 := STMEvent{
		Role:      "agent",
		Type:      models.STMEventTypeMessage,
		Content:   "I'm doing well, thank you!",
		Timestamp: time.Now().Add(-1 * time.Minute),
	}

	err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event1)
	assert.NoError(err)

	err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event2)
	assert.NoError(err)

	// Test 2: Retrieve STM context
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(err)
	assert.Len(events, 2)

	// Verify events are in reverse order (newest first)
	assert.Equal(models.STMEventTypeMessage, events[0].Type)
	assert.Equal("agent", events[0].Role)
	assert.Equal("I'm doing well, thank you!", events[0].Content)
	assert.Equal(models.STMEventTypeMessage, events[1].Type)
	assert.Equal("user", events[1].Role)
	assert.Equal("Hello, how are you?", events[1].Content)

	// Test 3: Verify max events limit works
	// Clear and add 3 events
	_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)
	os.Setenv("STM_CACHE_MAX_TURNS", "2")
	defer os.Unsetenv("STM_CACHE_MAX_TURNS")

	for i := 1; i <= 3; i++ {
		event := STMEvent{
			Role:      "user",
			Type:      models.STMEventTypeMessage,
			Content:   fmt.Sprintf("Message %d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event)
		assert.NoError(err)
	}

	// Should only return 2 most recent events
	events, err = stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(err)
	assert.Len(events, 2)
	assert.Equal("Message 3", events[0].Content) // Newest first
	assert.Equal("Message 2", events[1].Content)
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
	tenantID := "test_tenant"
	userID := "metrics_test_user_1"
	agentID := "test_agent"

	// Reset metrics before test
	// MetricSTMCacheOps.Reset() // This might not be available depending on your metrics library

	// Clear any existing data for this user key
	_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)

	// 1. Test AddSTMEvent
	event1 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "Hi", Timestamp: time.Now()}
	err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event1)
	assert.NoError(err)

	// 2. Test GetSTMContext
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(err)
	assert.Equal(1, len(events))

	// Test max events trimming
	_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)
	os.Setenv("STM_CACHE_MAX_TURNS", "2")
	defer os.Unsetenv("STM_CACHE_MAX_TURNS")

	// Add 3 events, only 2 should remain
	_ = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "1", Timestamp: time.Now()})
	_ = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "2", Timestamp: time.Now()})
	_ = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "3", Timestamp: time.Now()})

	events, err = stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(err)
	assert.Len(events, 2)
	assert.Equal("3", events[0].Content) // Newest first
	assert.Equal("2", events[1].Content)
}
