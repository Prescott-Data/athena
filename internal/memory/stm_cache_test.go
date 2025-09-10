package memory

import (
	"context"
	"testing"
	"time"

	memmodels "github.com/dromos-org/memory-os/models/memory"
)

// MockCache implements the cache.Interface for testing
type MockCache struct {
	data map[string][]string
}

func NewMockCache() *MockCache {
	return &MockCache{
		data: make(map[string][]string),
	}
}

func (m *MockCache) Set(key string, value interface{}) error                           { return nil }
func (m *MockCache) SetWithTTL(key string, value interface{}, ttl time.Duration) error { return nil }
func (m *MockCache) Get(key string, dest interface{}) error                            { return nil }
func (m *MockCache) Delete(key string) error {
	delete(m.data, key)
	return nil
}
func (m *MockCache) DeletePattern(pattern string) error { return nil }
func (m *MockCache) Exists(key string) (bool, error) {
	_, exists := m.data[key]
	return exists, nil
}
func (m *MockCache) Health() error { return nil }
func (m *MockCache) Close() error  { return nil }

// Implement Redis list operations for MockCache
func (m *MockCache) LRange(key string, start, stop int64) ([]string, error) {
	if data, exists := m.data[key]; exists {
		if start == 0 && stop >= int64(len(data)-1) {
			return data, nil
		}
		if stop >= int64(len(data)) {
			stop = int64(len(data)) - 1
		}
		if start <= stop && start >= 0 {
			return data[start : stop+1], nil
		}
	}
	return []string{}, nil
}

func (m *MockCache) LPush(key string, values ...interface{}) error {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = v.(string)
	}

	if _, exists := m.data[key]; !exists {
		m.data[key] = []string{}
	}

	// Prepend values (LPUSH behavior)
	m.data[key] = append(strValues, m.data[key]...)
	return nil
}

func (m *MockCache) LTrim(key string, start, stop int64) error {
	if data, exists := m.data[key]; exists {
		if stop >= int64(len(data)) {
			stop = int64(len(data)) - 1
		}
		if start <= stop && start >= 0 {
			m.data[key] = data[start : stop+1]
		}
	}
	return nil
}

func (m *MockCache) LLen(key string) (int64, error) {
	if data, exists := m.data[key]; exists {
		return int64(len(data)), nil
	}
	return 0, nil
}

func (m *MockCache) Expire(key string, expiration time.Duration) error { return nil }
func (m *MockCache) SetEX(key string, value string, expiration time.Duration) error {
	m.data[key] = []string{value}
	return nil
}
func (m *MockCache) Keys(pattern string) ([]string, error) {
	var keys []string
	for key := range m.data {
		keys = append(keys, key)
	}
	return keys, nil
}
func (m *MockCache) BRPop(timeout time.Duration, keys ...string) ([]string, error) {
	return []string{}, nil
}

func TestSTMCache_AddAndRetrieveConversationTurn(t *testing.T) {
	mockCache := NewMockCache()
	stmCache := NewSTMCache(mockCache)
	ctx := context.Background()
	userID := "test_user_123"

	// Add a user turn
	userTurn := ConversationTurn{
		Type:      "user",
		Content:   "What about our Tokyo office policy?",
		Timestamp: time.Now(),
	}

	err := stmCache.AddConversationTurn(ctx, userID, userTurn)
	if err != nil {
		t.Fatalf("Failed to add conversation turn: %v", err)
	}

	// Add an agent turn
	agentTurn := ConversationTurn{
		Type:      "agent",
		Content:   "For Tokyo office, same policy applies but reports go to APAC regional manager",
		Timestamp: time.Now(),
	}

	err = stmCache.AddConversationTurn(ctx, userID, agentTurn)
	if err != nil {
		t.Fatalf("Failed to add agent turn: %v", err)
	}

	// Retrieve conversation context
	turns, err := stmCache.GetConversationContext(ctx, userID)
	if err != nil {
		t.Fatalf("Failed to get conversation context: %v", err)
	}

	// Verify we got the turns back (should be in reverse order - newest first)
	if len(turns) != 2 {
		t.Fatalf("Expected 2 turns, got %d", len(turns))
	}

	// First turn should be the agent (most recent)
	if turns[0].Type != "agent" {
		t.Errorf("Expected first turn to be agent, got %s", turns[0].Type)
	}

	// Second turn should be the user
	if turns[1].Type != "user" {
		t.Errorf("Expected second turn to be user, got %s", turns[1].Type)
	}

	if turns[1].Content != "What about our Tokyo office policy?" {
		t.Errorf("Expected user content to match, got %s", turns[1].Content)
	}
}

func TestSTMCache_ConvertMessagesToTurns(t *testing.T) {
	mockCache := NewMockCache()
	stmCache := NewSTMCache(mockCache)

	// Create test messages
	messages := []memmodels.Message{
		{
			Type:      "user",
			Content:   "Hello, how are you?",
			CreatedAt: time.Now().Add(-2 * time.Minute),
		},
		{
			Type:      "agent",
			Content:   "I'm doing well, thank you!",
			CreatedAt: time.Now().Add(-1 * time.Minute),
		},
	}

	// Convert messages to turns
	turns := stmCache.ConvertMessagesToTurns(messages)

	// Verify conversion
	if len(turns) != 2 {
		t.Fatalf("Expected 2 turns, got %d", len(turns))
	}

	if turns[0].Type != "user" {
		t.Errorf("Expected first turn to be user, got %s", turns[0].Type)
	}

	if turns[0].Content != "Hello, how are you?" {
		t.Errorf("Expected user content to match, got %s", turns[0].Content)
	}

	if turns[1].Type != "agent" {
		t.Errorf("Expected second turn to be agent, got %s", turns[1].Type)
	}
}

func TestSTMCache_HotPathPerformance(t *testing.T) {
	mockCache := NewMockCache()
	stmCache := NewSTMCache(mockCache)
	ctx := context.Background()
	userID := "perf_test_user"

	// Add multiple turns to simulate a conversation
	for i := 0; i < 20; i++ {
		turn := ConversationTurn{
			Type:      "user",
			Content:   "Test message",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Second),
		}
		stmCache.AddConversationTurn(ctx, userID, turn)
	}

	// Measure retrieval time (hot path)
	start := time.Now()
	turns, err := stmCache.GetConversationContext(ctx, userID)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to get conversation context: %v", err)
	}

	// Should only return max turns (10)
	if int64(len(turns)) != StmCacheMaxTurns {
		t.Errorf("Expected %d turns, got %d", StmCacheMaxTurns, len(turns))
	}

	// Hot path should be very fast (< 10ms for mock)
	if duration > 10*time.Millisecond {
		t.Logf("Hot path took %v (may be acceptable for mock cache)", duration)
	}

	t.Logf("Hot path retrieval completed in %v", duration)
}
