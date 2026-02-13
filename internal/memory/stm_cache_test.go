package memory

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	// Load .env.dev from the project root (two levels up from internal/memory/)
	err := godotenv.Load("../../.env.dev")
	if err != nil {
		log.Fatalf("FATAL: Could not find .env.dev file at project root. Error: %v", err)
	} else {
		log.Println("INFO: Loaded .env.dev file for testing")
	}

	// Run all the tests in the package
	os.Exit(m.Run())
}

// setupRealSTMCache creates an STMCache backed by real Redis.
// Returns the cache and a cleanup function that clears the test key.
func setupRealSTMCache(t *testing.T, tenantID, userID, agentID string) *STMCache {
	t.Helper()

	redisClient, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: real Redis not available: %v", err)
	}

	stmCache := NewSTMCache(redisClient)

	// Clean up before and after the test
	ctx := context.Background()
	_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)

	t.Cleanup(func() {
		_ = stmCache.ClearSTMContext(ctx, tenantID, userID, agentID)
		redisClient.Close()
	})

	return stmCache
}

// setupRedisTestClient creates a new Redis client for testing.
// This is shared by all integration tests in this package.
func setupRedisTestClient() (cache.Interface, error) {
	client, err := cache.NewRedisClient()
	if err != nil {
		return nil, err
	}
	if err := client.Health(); err != nil {
		return nil, err
	}
	return client, nil
}

func TestSTMCache_AddAndRetrieveSTMEvent(t *testing.T) {
	tenantID := "test-tenant"
	userID := "test-user-add-retrieve"
	agentID := "test-agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	// 1. Add a user event
	userEvent := STMEvent{
		Role:      "user",
		Type:      models.STMEventTypeMessage,
		Content:   "Hello, world!",
		Timestamp: time.Now().UTC(),
	}
	err := stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, userEvent)
	assert.NoError(t, err)

	// 2. Add an agent event
	agentEvent := STMEvent{
		Role:      "agent",
		Type:      models.STMEventTypeMessage,
		Content:   "Hi there!",
		Timestamp: time.Now().UTC(),
	}
	err = stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, agentEvent)
	assert.NoError(t, err)

	// 3. Retrieve the context
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	// LIFO order: agent event (newest) is first
	assert.Equal(t, agentEvent.Content, events[0].Content)
	assert.Equal(t, "agent", events[0].Role)
	assert.Equal(t, userEvent.Content, events[1].Content)
	assert.Equal(t, "user", events[1].Role)
}

func TestSTMCache_Trim(t *testing.T) {
	tenantID := "test-tenant"
	userID := "test-user-trim"
	agentID := "test-agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	// Set max turns to 2 so the oldest event gets trimmed
	t.Setenv("STM_CACHE_MAX_TURNS", "2")

	// Add 3 events — Redis LPUSH + LTRIM(0,1) keeps only the 2 newest
	event1 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "1", Timestamp: time.Now().UTC()}
	event2 := STMEvent{Role: "agent", Type: models.STMEventTypeMessage, Content: "2", Timestamp: time.Now().UTC()}
	event3 := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "3", Timestamp: time.Now().UTC()}

	assert.NoError(t, stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event1))
	assert.NoError(t, stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event2))
	assert.NoError(t, stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event3))

	// Should only return 2 most recent events
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, "3", events[0].Content) // Newest
	assert.Equal(t, "2", events[1].Content) // Second newest
}

func TestSTMCache_EmptyContext(t *testing.T) {
	tenantID := "test-tenant"
	userID := "test-user-empty"
	agentID := "test-agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	// Retrieve context for a user with no events
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 0)
}

func TestSTMCache_UpdateSTMEntries(t *testing.T) {
	tenantID := "test-tenant"
	userID := "test-user-update"
	agentID := "test-agent"
	stmCache := setupRealSTMCache(t, tenantID, userID, agentID)
	ctx := context.Background()

	// First, add some initial events
	initialEvent := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "initial", Timestamp: time.Now().UTC()}
	assert.NoError(t, stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, initialEvent))

	// Now replace with a completely new set of events via UpdateSTMEntries
	eventsToUpdate := []STMEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "older", Timestamp: time.Now().Add(-1 * time.Minute).UTC()},
		{Role: "agent", Type: models.STMEventTypeMessage, Content: "newer", Timestamp: time.Now().UTC()},
	}

	err := stmCache.UpdateSTMEntries(ctx, tenantID, userID, agentID, eventsToUpdate)
	assert.NoError(t, err)

	// Verify the cache now contains exactly the updated events in correct order
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	// UpdateSTMEntries pushes in reverse order via LPUSH, so newest ends up first
	assert.Equal(t, "older", events[0].Content)
	assert.Equal(t, "newer", events[1].Content)
}

func TestSTMCache_KeyScoping(t *testing.T) {
	ctx := context.Background()
	tenantA := "tenant-a"
	tenantB := "tenant-b"
	userID := "same-user"
	agentID := "same-agent"

	// Create two caches for different tenants (sharing the same Redis)
	stmCacheA := setupRealSTMCache(t, tenantA, userID, agentID)
	stmCacheB := setupRealSTMCache(t, tenantB, userID, agentID)

	// Add an event to tenant A
	eventA := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "tenant A message", Timestamp: time.Now().UTC()}
	assert.NoError(t, stmCacheA.AddSTMEvent(ctx, tenantA, userID, agentID, eventA))

	// Add a different event to tenant B
	eventB := STMEvent{Role: "user", Type: models.STMEventTypeMessage, Content: "tenant B message", Timestamp: time.Now().UTC()}
	assert.NoError(t, stmCacheB.AddSTMEvent(ctx, tenantB, userID, agentID, eventB))

	// Retrieve tenant A — should only see tenant A's event
	eventsA, err := stmCacheA.GetSTMContext(ctx, tenantA, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, eventsA, 1)
	assert.Equal(t, "tenant A message", eventsA[0].Content)

	// Retrieve tenant B — should only see tenant B's event
	eventsB, err := stmCacheB.GetSTMContext(ctx, tenantB, userID, agentID)
	assert.NoError(t, err)
	assert.Len(t, eventsB, 1)
	assert.Equal(t, "tenant B message", eventsB[0].Content)
}
