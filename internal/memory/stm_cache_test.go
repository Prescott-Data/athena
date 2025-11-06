package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)



// Test Helpers
func setupSTMCacheTest() (*STMCache, *MockRedis) {
	mockRedis := new(MockRedis)
	stmCache := NewSTMCache(mockRedis)
	return stmCache, mockRedis
}

func TestSTMCache_AddAndRetrieveSTMEvent(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	userID := "test-user-123"
	cacheKey := stmCache.generateSTMKey(userID)

	// 1. Add a user event
	userEvent := STMEvent{
		Role:      "user",
		Type:      STMEventTypeMessage,
		Content:   "Hello, world!",
		Timestamp: time.Now().UTC(),
	}
	userEventJSON, _ := json.Marshal(userEvent)

	mockRedis.On("LPush", cacheKey, []interface{}{string(userEventJSON)}).Return(nil).Once()
	mockRedis.On("LTrim", cacheKey, int64(0), StmCacheMaxTurns-1).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, StmCacheTTL).Return(nil).Once()

	err := stmCache.AddSTMEvent(ctx, userID, userEvent)
	assert.NoError(t, err)

	// 2. Add an agent event
	agentEvent := STMEvent{
		Role:      "agent",
		Type:      STMEventTypeMessage,
		Content:   "Hi there!",
		Timestamp: time.Now().UTC(),
	}
	agentEventJSON, _ := json.Marshal(agentEvent)

	mockRedis.On("LPush", cacheKey, []interface{}{string(agentEventJSON)}).Return(nil).Once()
	mockRedis.On("LTrim", cacheKey, int64(0), StmCacheMaxTurns-1).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, StmCacheTTL).Return(nil).Once()

	err = stmCache.AddSTMEvent(ctx, userID, agentEvent)
	assert.NoError(t, err)

	// 3. Retrieve the context
	expectedRawEvents := []string{string(agentEventJSON), string(userEventJSON)}
	mockRedis.On("LRange", cacheKey, int64(0), StmCacheMaxTurns-1).Return(expectedRawEvents, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, userID)
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
	userID := "test-user-trim"
	cacheKey := stmCache.generateSTMKey(userID)

	// Set max turns to a smaller number for this test
	originalMaxTurns := StmCacheMaxTurns
	StmCacheMaxTurns = 2
	defer func() { StmCacheMaxTurns = originalMaxTurns }()

	// Add 3 events, expecting the oldest to be trimmed
	event1 := STMEvent{Role: "user", Type: STMEventTypeMessage, Content: "1"}
	event2 := STMEvent{Role: "agent", Type: STMEventTypeMessage, Content: "2"}
	event3 := STMEvent{Role: "user", Type: STMEventTypeMessage, Content: "3"}

	event2JSON, _ := json.Marshal(event2)
	event3JSON, _ := json.Marshal(event3)

	// Mock calls for all 3 additions
	mockRedis.On("LPush", cacheKey, mock.Anything).Return(nil)
	mockRedis.On("LTrim", cacheKey, int64(0), StmCacheMaxTurns-1).Return(nil)
	mockRedis.On("Expire", cacheKey, StmCacheTTL).Return(nil)

	stmCache.AddSTMEvent(ctx, userID, event1)
	stmCache.AddSTMEvent(ctx, userID, event2)
	stmCache.AddSTMEvent(ctx, userID, event3)

	// Now, mock the retrieval
	expectedRawEvents := []string{string(event3JSON), string(event2JSON)}
	mockRedis.On("LRange", cacheKey, int64(0), StmCacheMaxTurns-1).Return(expectedRawEvents, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, userID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, "3", events[0].Content) // Newest
	assert.Equal(t, "2", events[1].Content) // Second newest

	mockRedis.AssertExpectations(t)
}

func TestSTMCache_EmptyContext(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	userID := "test-user-empty"
	cacheKey := stmCache.generateSTMKey(userID)

	mockRedis.On("LRange", cacheKey, int64(0), StmCacheMaxTurns-1).Return([]string{}, nil).Once()

	events, err := stmCache.GetSTMContext(ctx, userID)
	assert.NoError(t, err)
	assert.Len(t, events, 0)

	mockRedis.AssertExpectations(t)
}

func TestSTMCache_UpdateSTMEntries(t *testing.T) {
	stmCache, mockRedis := setupSTMCacheTest()
	ctx := context.Background()
	userID := "test-user-update"
	cacheKey := stmCache.generateSTMKey(userID)

	eventsToUpdate := []STMEvent{
		{Role: "user", Type: STMEventTypeMessage, Content: "older"},
		{Role: "agent", Type: STMEventTypeMessage, Content: "newer"},
	}

	// Mock the delete call
	mockRedis.On("Delete", cacheKey).Return(nil).Once()

	// Mock the LPush calls (in reverse order)
	newerJSON, _ := json.Marshal(eventsToUpdate[1])
	olderJSON, _ := json.Marshal(eventsToUpdate[0])
	mockRedis.On("LPush", cacheKey, []interface{}{string(newerJSON)}).Return(nil).Once()
	mockRedis.On("LPush", cacheKey, []interface{}{string(olderJSON)}).Return(nil).Once()

	// Mock the final trim and expire
	mockRedis.On("LTrim", cacheKey, int64(0), StmCacheMaxTurns-1).Return(nil).Once()
	mockRedis.On("Expire", cacheKey, StmCacheTTL).Return(nil).Once()

	err := stmCache.UpdateSTMEntries(ctx, userID, eventsToUpdate)
	assert.NoError(t, err)

	mockRedis.AssertExpectations(t)
}