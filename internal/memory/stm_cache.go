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
	// StmCacheMaxTurns is the maximum number of conversation turns to keep in memory
	StmCacheMaxTurns int64
	// StmCacheTTL is the default TTL for STM cache entries
	StmCacheTTL time.Duration
)

// ConversationTurn represents a single turn in a conversation for STM caching
type ConversationTurn struct {
	Type      string    `json:"type"`      // "user" or "agent"
	Content   string    `json:"content"`   // The message content
	Timestamp time.Time `json:"timestamp"` // When the turn occurred
}

// STMCache provides Short-Term Memory caching for conversations
type STMCache struct {
	redis cache.Interface
}

// NewSTMCache creates a new STM cache instance
func NewSTMCache(redisClient cache.Interface) *STMCache {
	return &STMCache{
		redis: redisClient,
	}
}

// GetConversationContext retrieves the last N conversation turns from Redis cache
func (s *STMCache) GetConversationContext(ctx context.Context, userID string) ([]ConversationTurn, error) {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Use Redis LRANGE to get the last 10 turns instantly
	rawTurns, err := s.redis.LRange(cacheKey, 0, StmCacheMaxTurns-1)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("get", "error").Inc()
		log.Printf("WARN: Failed to retrieve STM cache for user %s: %v", userID, err)
		return []ConversationTurn{}, nil // Return empty on cache miss, don't fail
	}

	turns := make([]ConversationTurn, 0, len(rawTurns))
	for _, rawTurn := range rawTurns {
		var turn ConversationTurn
		if err := json.Unmarshal([]byte(rawTurn), &turn); err != nil {
			log.Printf("WARN: Failed to unmarshal conversation turn for user %s: %v", userID, err)
			continue
		}
		turns = append(turns, turn)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache retrieval completed - UserID: %s, Duration: %v, Turns: %d",
		userID, duration, len(turns))
	MetricSTMCacheOps.WithLabelValues("get", "ok").Inc()

	return turns, nil
}

// AddConversationTurn adds a new conversation turn to the STM cache
func (s *STMCache) AddConversationTurn(ctx context.Context, userID string, turn ConversationTurn) error {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Serialize the turn
	turnJSON, err := json.Marshal(turn)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to marshal conversation turn: %w", err)
	}

	// LPUSH the new turn to the front of the list
	if err := s.redis.LPush(cacheKey, string(turnJSON)); err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to push conversation turn: %w", err)
	}

	// LTRIM to maintain sliding window of max turns
	if err := s.redis.LTrim(cacheKey, 0, StmCacheMaxTurns-1); err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to trim conversation turns: %w", err)
	}

	// Set expiration on the key to auto-cleanup stale conversations
	if err := s.redis.Expire(cacheKey, StmCacheTTL); err != nil {
		log.Printf("WARN: Failed to set expiration on STM cache for user %s: %v", userID, err)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache turn added - UserID: %s, Duration: %v, Type: %s",
		userID, duration, turn.Type)
	MetricSTMCacheOps.WithLabelValues("append", "ok").Inc()

	return nil
}

// UpdateConversationTurns updates the cache with a complete conversation context
func (s *STMCache) UpdateConversationTurns(ctx context.Context, userID string, turns []ConversationTurn) error {
	start := time.Now()

	cacheKey := s.generateSTMKey(userID)

	// Clear existing cache
	if err := s.redis.Delete(cacheKey); err != nil {
		log.Printf("WARN: Failed to clear existing STM cache for user %s: %v", userID, err)
	}

	// Add turns in reverse order (newest first) to maintain chronological order
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		turnJSON, err := json.Marshal(turn)
		if err != nil {
			log.Printf("WARN: Failed to marshal turn for user %s: %v", userID, err)
			continue
		}

		if err := s.redis.LPush(cacheKey, string(turnJSON)); err != nil {
			log.Printf("WARN: Failed to push turn to cache for user %s: %v", userID, err)
			continue
		}
	}

	// Ensure we don't exceed max turns
	if err := s.redis.LTrim(cacheKey, 0, StmCacheMaxTurns-1); err != nil {
		log.Printf("WARN: Failed to trim STM cache for user %s: %v", userID, err)
	}

	// Set expiration
	if err := s.redis.Expire(cacheKey, StmCacheTTL); err != nil {
		log.Printf("WARN: Failed to set expiration on STM cache for user %s: %v", userID, err)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM cache updated - UserID: %s, Duration: %v, Turns: %d",
		userID, duration, len(turns))

	return nil
}

// ClearConversationContext clears the STM cache for a user
func (s *STMCache) ClearConversationContext(ctx context.Context, userID string) error {
	cacheKey := s.generateSTMKey(userID)
	return s.redis.Delete(cacheKey)
}

// ConvertMessagesToTurns converts database messages to conversation turns
func (s *STMCache) ConvertMessagesToTurns(messages []memmodels.Message) []ConversationTurn {
	turns := make([]ConversationTurn, 0, len(messages))

	for _, msg := range messages {
		turn := ConversationTurn{
			Type:      msg.Type,
			Content:   msg.Content,
			Timestamp: msg.CreatedAt,
		}
		turns = append(turns, turn)
	}

	return turns
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
