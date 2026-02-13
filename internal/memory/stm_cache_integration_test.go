package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/stretchr/testify/assert"
)

// TestSTMCache_Integration_BulkAdd validates adding many events and
// verifying the sliding window trim works correctly in a real Redis.
func TestSTMCache_Integration_BulkAdd(t *testing.T) {
	tenantID := "test_tenant"
	userID := "integration_bulk_user"
	agentID := "test_agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	t.Setenv("STM_CACHE_MAX_TURNS", "5")

	// Add 10 events — only the last 5 should survive the sliding window
	for i := 1; i <= 10; i++ {
		event := STMEvent{
			Role:      "user",
			Type:      models.STMEventTypeMessage,
			Content:   fmt.Sprintf("Message %d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		err := stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event)
		assert.NoError(t, err)
	}

	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 5)

	// Verify we have the 5 newest (10 down to 6)
	assert.Equal(t, "Message 10", events[0].Content)
	assert.Equal(t, "Message 9", events[1].Content)
	assert.Equal(t, "Message 8", events[2].Content)
	assert.Equal(t, "Message 7", events[3].Content)
	assert.Equal(t, "Message 6", events[4].Content)
}

// TestSTMCache_Integration_CacheStats validates the GetCacheStats method against real Redis.
func TestSTMCache_Integration_CacheStats(t *testing.T) {
	tenantID := "test_tenant"
	userID := "integration_stats_user"
	agentID := "test_agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	// Stats on empty cache
	stats, err := stmCache.GetCacheStats(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Equal(t, false, stats["exists"])
	assert.Equal(t, 0, stats["length"])

	// Add 3 events
	for i := 0; i < 3; i++ {
		event := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: fmt.Sprintf("msg %d", i), Timestamp: time.Now()}
		assert.NoError(t, stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event))
	}

	// Stats should reflect the 3 events
	stats, err = stmCache.GetCacheStats(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Equal(t, true, stats["exists"])
	assert.Equal(t, int64(3), stats["length"])
}
