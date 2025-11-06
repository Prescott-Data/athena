package memory

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestMain(m *testing.M) {
	// Explicitly load the .env.dev file from the project root (../)
	err := godotenv.Load("/home/dev/projects/dromos-core/memory-os/.env.dev")
	if err != nil {
		// This is critical. It stops all tests if the env file is missing.
		log.Fatalf("FATAL: Could not find .env.dev file. Make sure you are running tests from the project root. Error: %v", err)
	} else {
		log.Println("INFO: Loaded .env.dev file for testing")
	}

	// Run all the tests in the package
	os.Exit(m.Run())
}

// Test Helpers
func setupSTMCacheTest() (*STMCache, *MockRedis) {
	mockRedis := new(MockRedis)
	stmCache := NewSTMCache(mockRedis)
	return stmCache, mockRedis
}

func TestSTMCache_AddAndRetrieveSTMEvent(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	tenantID := "test-tenant"
	userID := "test-user-123"
	agentID := "test-agent"
	cacheKey := stmCache.generateSTMKeyWithScope(tenantID, userID, agentID)

	// 1. Add a user event
	userEvent := STMEvent{
		Role:      "user",
		Type:      models.STMEventTypeMessage,
		Content:   "Hello, world!",
		Timestamp: time.Now().UTC(),
	}
	userEventJSON, _ := json.Marshal(userEvent)

	mockRedis.On("LPush", cacheKey, []interface{}{string(userEventJSON)}).Return(nil).Once()
	mockRedis.On("LTrim", cacheKey, int64(0), int64(10-1)).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, time.Duration(2)*time.Hour).Return(nil).Once()

	err := stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, userEvent)
	assert.NoError(t, err)

	// 2. Add an agent event
	agentEvent := STMEvent{
		Role:      "agent",
		Type:      models.STMEventTypeMessage,
		Content:   "Hi there!",
		Timestamp: time.Now().UTC(),
	}
	agentEventJSON, _ := json.Marshal(agentEvent)

	mockRedis.On("LPush", cacheKey, []interface{}{string(agentEventJSON)}).Return(nil).Once()
	mockRedis.On("LTrim", cacheKey, int64(0), int64(10-1)).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, time.Duration(2)*time.Hour).Return(nil).Once()

	err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, agentEvent)
	assert.NoError(t, err)

	// 3. Retrieve the context
	expectedRawEvents := []string{string(agentEventJSON), string(userEventJSON)}
	mockRedis.On("LRange", cacheKey, int64(0), int64(10-1)).Return(expectedRawEvents, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	// Note: LIFO order, so agent event is first
	assert.Equal(t, agentEvent.Content, events[0].Content)
	assert.Equal(t, userEvent.Content, events[1].Content)

	mockRedis.AssertExpectations(t)
}

func TestSTMCache_Trim(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	tenantID := "test-tenant"
	userID := "test-user-trim"
	agentID := "test-agent"
	cacheKey := stmCache.generateSTMKeyWithScope(tenantID, userID, agentID)

	// --- THIS IS THE FIX ---
	// Temporarily set the environment variable *for this test only*.
	// This ensures that parseIntEnv() in the *real code* will read "2".
	t.Setenv("STM_CACHE_MAX_TURNS", "2")

	// Now we can set our expected max turns to 2
	expectedMaxTurns := int64(2)
	// --- END OF FIX ---

	// Add 3 events, expecting the oldest to be trimmed
	event1 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "1"}
	event2 := STMEvent{Role: "agent", Type: models.STMEventTypeMessage, Content: "2"}
	event3 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "3"}

	event2JSON, _ := json.Marshal(event2)
	event3JSON, _ := json.Marshal(event3)

	// Mock calls for all 3 additions
	mockRedis.On("LPush", cacheKey, mock.Anything).Return(nil)
	// The mock now correctly expects LTrim(..., 0, 1) because the code will read "2"
	mockRedis.On("LTrim", cacheKey, int64(0), expectedMaxTurns-1).Return(nil)
	mockRedis.On("Expire", cacheKey, time.Duration(2)*time.Hour).Return(nil)

	stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event1)
	stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event2)
	stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event3)

	// Now, mock the retrieval
	expectedRawEvents := []string{string(event3JSON), string(event2JSON)}
	// The mock now correctly expects LRange(..., 0, 1)
	mockRedis.On("LRange", cacheKey, int64(0), expectedMaxTurns-1).Return(expectedRawEvents, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, "3", events[0].Content) // Newest
	assert.Equal(t, "2", events[1].Content) // Second newest

	mockRedis.AssertExpectations(t)
}

func TestSTMCache_EmptyContext(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	tenantID := "test-tenant"
	userID := "test-user-empty"
	agentID := "test-agent"
	cacheKey := stmCache.generateSTMKeyWithScope(tenantID, userID, agentID)

	mockRedis.On("LRange", cacheKey, int64(0), int64(10-1)).Return([]string{}, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 0)

	mockRedis.AssertExpectations(t)
}

func TestSTMCache_UpdateSTMEntries(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	tenantID := "test-tenant"
	userID := "test-user-update"
	agentID := "test-agent"
	cacheKey := stmCache.generateSTMKeyWithScope(tenantID, userID, agentID)

	eventsToUpdate := []STMEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "older"},
		{Role: "agent", Type: models.STMEventTypeMessage, Content: "newer"},
	}

	// Mock the delete call
	mockRedis.On("Delete", cacheKey).Return(nil).Once()

	// Mock the LPush calls (in reverse order)
	newerJSON, _ := json.Marshal(eventsToUpdate[1])
	olderJSON, _ := json.Marshal(eventsToUpdate[0])
	mockRedis.On("LPush", cacheKey, []interface{}{string(newerJSON)}).Return(nil).Once()
	mockRedis.On("LPush", cacheKey, []interface{}{string(olderJSON)}).Return(nil).Once()

	// Mock the final trim and expire
	mockRedis.On("LTrim", cacheKey, int64(0), int64(10-1)).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, time.Duration(2)*time.Hour).Return(nil).Once()

	err := stmCache.UpdateSTMEntries(ctx, tenantID, userID, agentID, eventsToUpdate)
	assert.NoError(t, err)

	mockRedis.AssertExpectations(t)
}
