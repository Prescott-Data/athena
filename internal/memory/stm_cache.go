package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/models"
	memmodels "bitbucket.org/dromos/memory-os/models/memory"

	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

func init() {
	// Parse TTL hours
	ttlHours := 2 // default to 2 hours
	if ttlStr := os.Getenv("STM_CACHE_TTL"); ttlStr != "" {
		if parsed, err := strconv.Atoi(ttlStr); err == nil {
			ttlHours = parsed
		} else {
			log.Printf("WARN: Invalid STM_CACHE_TTL value: %s, using default: %d", ttlStr, ttlHours)
		}
	}

	stmCacheMaxTurns := 10 // default to 10 turns
	if maxTurnsStr := os.Getenv("STM_CACHE_MAX_TURNS"); maxTurnsStr != "" {
		if parsed, err := strconv.Atoi(maxTurnsStr); err == nil {
			stmCacheMaxTurns = parsed
		} else {
			log.Printf("WARN: Invalid STM_CACHE_MAX_TURNS value: %s, using default: %d", maxTurnsStr, stmCacheMaxTurns)
		}
	}

	// Set default key prefix if not provided
	if StmCacheKeyPrefix == "" {
		StmCacheKeyPrefix = "stm_cache"
	}

	// Set the global variables after successful parsing
	StmCacheMaxTurns = int64(stmCacheMaxTurns)
	StmCacheTTL = time.Duration(ttlHours) * time.Hour
}

var (
	// StmCacheKeyPrefix is the prefix for all STM cache keys
	StmCacheKeyPrefix = os.Getenv("STM_CACHE_KEY_PREFIX")
	// StmCacheMaxTurns is the maximum number of conversation events to keep in memory
	StmCacheMaxTurns int64
	// StmCacheTTL is the default TTL for STM cache entries
	StmCacheTTL time.Duration
)

// STMEvent represents a single event in the short-term memory cache.
// This can be a conversation turn, an agent's thought, an action, or an observation.
type STMEvent struct {
	Role      string                `json:"role"`      // Who the event belongs to (e.g., "user", "agent")
	Type      models.STMEventType   `json:"type"`      // Type of the event (e.g., "message", "thought")
	Content   string                `json:"content"`   // The content of the event
	Timestamp time.Time             `json:"timestamp"` // When the event occurred
}

// STMCache provides Short-Term Memory caching for conversations and agent events.

type STMCache struct {
	redis cache.Interface
}

// NewSTMCache creates a new STM cache instance
func NewSTMCache(redisClient cache.Interface) *STMCache {
	return &STMCache{
		redis: redisClient,
	}
}

// GetSTMContext retrieves the last N events from the Redis cache.
func (s *STMCache) GetSTMContext(ctx context.Context, userID string) ([]STMEvent, error) {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Use Redis LRANGE to get the last N events instantly
	rawEvents, err := s.redis.LRange(cacheKey, 0, StmCacheMaxTurns-1)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("get", "error").Inc()
		log.Printf("WARN: Failed to retrieve STM cache for user %s: %v", userID, err)
		return []STMEvent{}, nil // Return empty on cache miss, don't fail
	}

	events := make([]STMEvent, 0, len(rawEvents))
	for _, rawEvent := range rawEvents {
		var event STMEvent
		if err := json.Unmarshal([]byte(rawEvent), &event); err != nil {
			log.Printf("WARN: Failed to unmarshal STM event for user %s: %v", userID, err)
			continue
		}
		events = append(events, event)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache retrieval completed - UserID: %s, Duration: %v, Events: %d",
		userID, duration, len(events))
	MetricSTMCacheOps.WithLabelValues("get", "ok").Inc()

	return events, nil
}

// AddSTMEvent adds a new event to the STM cache.
func (s *STMCache) AddSTMEvent(ctx context.Context, userID string, event STMEvent) error {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Serialize the event
	eventJSON, err := json.Marshal(event)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to marshal STM event: %w", err)
	}

	// LPUSH the new event to the front of the list
	if err := s.redis.LPush(cacheKey, string(eventJSON)); err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to push STM event: %w", err)
	}

	// LTRIM to maintain sliding window of max events
	if err := s.redis.LTrim(cacheKey, 0, StmCacheMaxTurns-1); err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to trim STM events: %w", err)
	}

	// Set expiration on the key to auto-cleanup stale conversations
	if err := s.redis.Expire(cacheKey, StmCacheTTL); err != nil {
		log.Printf("WARN: Failed to set expiration on STM cache for user %s: %v", userID, err)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache event added - UserID: %s, Duration: %v, Type: %s",
		userID, duration, event.Type)
	MetricSTMCacheOps.WithLabelValues("append", "ok").Inc()

	return nil
}

// UpdateSTMEntries updates the cache with a complete set of STM events.
func (s *STMCache) UpdateSTMEntries(ctx context.Context, userID string, events []STMEvent) error {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Clear existing cache
	if err := s.redis.Delete(cacheKey); err != nil {
		log.Printf("WARN: Failed to clear existing STM cache for user %s: %v", userID, err)
	}

	// Add events in reverse order (newest first) to maintain chronological order
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		eventJSON, err := json.Marshal(event)
		if err != nil {
			log.Printf("WARN: Failed to marshal event for user %s: %v", userID, err)
			continue
		}

		if err := s.redis.LPush(cacheKey, string(eventJSON)); err != nil {
			log.Printf("WARN: Failed to push event to cache for user %s: %v", userID, err)
			continue
		}
	}

	// Ensure we don't exceed max events
	if err := s.redis.LTrim(cacheKey, 0, StmCacheMaxTurns-1); err != nil {
		log.Printf("WARN: Failed to trim STM cache for user %s: %v", userID, err)
	}

	// Set expiration
	if err := s.redis.Expire(cacheKey, StmCacheTTL); err != nil {
		log.Printf("WARN: Failed to set expiration on STM cache for user %s: %v", userID, err)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache updated - UserID: %s, Duration: %v, Events: %d",
		userID, duration, len(events))

	return nil
}

// ClearSTMContext clears the STM cache for a user.
func (s *STMCache) ClearSTMContext(ctx context.Context, userID string) error {
	cacheKey := s.generateSTMKey(userID)
	return s.redis.Delete(cacheKey)
}

// ConvertMessagesToSTMEvents converts database messages to STM events.
func (s *STMCache) ConvertMessagesToSTMEvents(messages []memmodels.Message) []STMEvent {
	events := make([]STMEvent, 0, len(messages))

	for _, msg := range messages {
		event := STMEvent{
			Role:      msg.Type,
			Type:      models.STMEventTypeMessage,
			Content:   msg.Content,
			Timestamp: msg.CreatedAt,
		}
		events = append(events, event)
	}

	return events
}

// generateSTMKey creates the Redis key for a user's STM cache
func (s *STMCache) generateSTMKey(userID string) string {
	return fmt.Sprintf("%s:user_%s", StmCacheKeyPrefix, userID)
}

// generateSTMKeyWithScope creates the Redis key for a user's STM cache with tenant/agent scope
func (s *STMCache) generateSTMKeyWithScope(tenantID, userID, agentID string) string {
	return fmt.Sprintf("%s:v1:%s:%s:%s:user_%s", StmCacheKeyPrefix, tenantID, userID, agentID, userID)
}

// GetCacheStats returns statistics about the STM cache for a user
func (s *STMCache) GetCacheStats(ctx context.Context, userID string) (map[string]interface{}, error) {
	cacheKey := s.generateSTMKey(userID)

	exists, err := s.redis.Exists(cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to check cache existence: %w", err)
	}

	if !exists {
		return map[string]interface{}{
			"exists": false,
			"length": 0,
		}, nil
	}

	length, err := s.redis.LLen(cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache length: %w", err)
	}

	return map[string]interface{}{
		"exists":    true,
		"length":    length,
		"max_turns": StmCacheMaxTurns,
		"ttl_hours": StmCacheTTL.Hours(),
	}, nil
}
