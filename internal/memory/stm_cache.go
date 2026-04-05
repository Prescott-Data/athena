package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/models"
	memmodels "github.com/Prescott-Data/athena/models/memory"

	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

// parseDurationEnv reads a time.Duration from env (in hours) with default
func parseDurationEnv(key string, defHours int) time.Duration {
	ttlHours := defHours
	if ttlStr := os.Getenv(key); ttlStr != "" {
		if parsed, err := strconv.Atoi(ttlStr); err == nil {
			ttlHours = parsed
		} else {
			slog.Warn("Invalid TTL value", slog.String("key", key), slog.String("value", ttlStr), slog.Int("default_hours", defHours))
		}
	}
	return time.Duration(ttlHours) * time.Hour
}

// STMEvent represents a single event in the short-term memory cache.
// This can be a conversation turn, an agent's thought, an action, or an observation.
type STMEvent struct {
	Role         string              `json:"role"`    // Who the event belongs to (e.g., "user", "agent")
	Type         models.STMEventType `json:"type"`    // Type of the event (e.g., "message", "thought")
	Content      string              `json:"content"` // The content of the event
	BlobURI      string              `json:"blobUri,omitempty"`
	BlobMimeType string              `json:"blobMimeType,omitempty"`
	Timestamp    time.Time           `json:"timestamp"`          // When the event occurred
	Metadata     map[string]string   `json:"metadata,omitempty"` // Optional metadata (origin_service, context_type, etc.)
	// ChainID ties this event to its session chain so the worker can associate
	// events with the correct chain without generating a new random ID.
	ChainID string `json:"chainId,omitempty"`
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
func (s *STMCache) GetSTMContext(ctx context.Context, tenantID, userID, agentID string) ([]STMEvent, error) {
	start := time.Now()

	StmCacheMaxTurns := int64(parseIntEnv("STM_CACHE_MAX_TURNS", 10))
	cacheKey := s.generateSTMKeyWithScope(tenantID, userID, agentID)

	// Use Redis LRANGE to get the last N events instantly
	rawEvents, err := s.redis.LRange(cacheKey, 0, StmCacheMaxTurns-1)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("get", "error").Inc()
		slog.Warn("Failed to retrieve STM cache", slog.String("user_id", userID), slog.String("error", err.Error()))
		return []STMEvent{}, nil // Return empty on cache miss, don't fail
	}

	events := make([]STMEvent, 0, len(rawEvents))
	for _, rawEvent := range rawEvents {
		var event STMEvent
		if err := json.Unmarshal([]byte(rawEvent), &event); err != nil {
			slog.Warn("Failed to unmarshal STM event", slog.String("user_id", userID), slog.String("error", err.Error()))
			continue
		}
		events = append(events, event)
	}

	duration := time.Since(start)
	slog.Info("STM cache retrieval completed",
		slog.String("user_id", userID),
		slog.Duration("duration", duration),
		slog.Int("event_count", len(events)),
	)
	MetricSTMCacheOps.WithLabelValues("get", "ok").Inc()

	return events, nil
}

// AddSTMEvent adds a new event to the STM cache.
func (s *STMCache) AddSTMEvent(ctx context.Context, tenantID, userID, agentID string, event STMEvent) error {
	start := time.Now()

	StmCacheMaxTurns := int64(parseIntEnv("STM_CACHE_MAX_TURNS", 10))
	StmCacheTTL := parseDurationEnv("STM_CACHE_TTL", 2) // 2 hours default
	cacheKey := s.generateSTMKeyWithScope(tenantID, userID, agentID)

	// Serialize the event
	eventJSON, err := json.Marshal(event)
	if err != nil {
		MetricSTMCacheOps.WithLabelValues("append", "error").Inc()
		return fmt.Errorf("failed to marshal STM event: %w", err)
	}

	// Coalescing Logic: Prevent high-frequency automation logs from flooding the cache
	if event.Type == models.STMEventTypeObservation {
		if execID, ok := event.Metadata["execution_id"]; ok && execID != "" {
			// Retrieve the most recent event
			lastEventJSON, err := s.redis.LIndex(cacheKey, 0)
			if err == nil && lastEventJSON != "" {
				var lastEvent STMEvent
				if err := json.Unmarshal([]byte(lastEventJSON), &lastEvent); err == nil {
					// Check if the recent event is also an observation with the same execution_id.
					// Only coalesce if the step_id is also the same — different step_ids are
					// sequential workflow steps and must be kept as separate events.
					if lastEvent.Type == models.STMEventTypeObservation {
						newStepID := event.Metadata["step_id"]
						lastStepID := lastEvent.Metadata["step_id"]
						sameStep := newStepID == "" || newStepID == lastStepID
						if lastExecID, ok := lastEvent.Metadata["execution_id"]; ok && lastExecID == execID && sameStep {
							// Coalesce: Update content and merge metadata
							lastEvent.Content = event.Content
							lastEvent.Timestamp = event.Timestamp

							if lastEvent.Metadata == nil {
								lastEvent.Metadata = make(map[string]string)
							}
							for k, v := range event.Metadata {
								lastEvent.Metadata[k] = v
							}

							// Increment coalesced_count
							currentCount := 1
							if countStr, exists := lastEvent.Metadata["coalesced_count"]; exists {
								if c, err := strconv.Atoi(countStr); err == nil {
									currentCount = c
								}
							}
							newCount := currentCount + 1
							lastEvent.Metadata["coalesced_count"] = strconv.Itoa(newCount)

							// Save the modified event back to the head of the list
							updatedJSON, err := json.Marshal(lastEvent)
							if err == nil {
								if err := s.redis.LSet(cacheKey, 0, string(updatedJSON)); err == nil {
									// Extend expiration
									_ = s.redis.Expire(cacheKey, StmCacheTTL)

									duration := time.Since(start)
									slog.Info("STM cache event coalesced",
										slog.String("user_id", userID),
										slog.Duration("duration", duration),
										slog.Int("coalesced_count", newCount),
									)
									MetricSTMCacheOps.WithLabelValues("append", "coalesced").Inc()
									return nil
								}
							}
						}
					}
				}
			}
		}
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
		slog.Warn("Failed to set expiration on STM cache", slog.String("user_id", userID), slog.String("error", err.Error()))
	}

	duration := time.Since(start)
	slog.Info("STM cache event added",
		slog.String("user_id", userID),
		slog.Duration("duration", duration),
		slog.String("type", string(event.Type)),
	)
	MetricSTMCacheOps.WithLabelValues("append", "ok").Inc()

	return nil
}

// UpdateSTMEntries updates the cache with a complete set of STM events.
func (s *STMCache) UpdateSTMEntries(ctx context.Context, tenantID, userID, agentID string, events []STMEvent) error {
	start := time.Now()

	StmCacheTTL := parseDurationEnv("STM_CACHE_TTL", 2) // 2 hours default

	StmCacheMaxTurns := int64(parseIntEnv("STM_CACHE_MAX_TURNS", 10))

	cacheKey := s.generateSTMKeyWithScope(tenantID, userID, agentID)

	// Clear existing cache
	if err := s.redis.Delete(cacheKey); err != nil {
		slog.Warn("Failed to clear existing STM cache", slog.String("user_id", userID), slog.String("error", err.Error()))
	}

	// Add events in reverse order (newest first) to maintain chronological order
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		eventJSON, err := json.Marshal(event)
		if err != nil {
			slog.Warn("Failed to marshal event", slog.String("user_id", userID), slog.String("error", err.Error()))
			continue
		}

		if err := s.redis.LPush(cacheKey, string(eventJSON)); err != nil {
			slog.Warn("Failed to push event to cache", slog.String("user_id", userID), slog.String("error", err.Error()))
			continue
		}
	}

	// Ensure we don't exceed max events
	if err := s.redis.LTrim(cacheKey, 0, StmCacheMaxTurns-1); err != nil {
		slog.Warn("Failed to trim STM cache", slog.String("user_id", userID), slog.String("error", err.Error()))
	}

	// Set expiration
	if err := s.redis.Expire(cacheKey, StmCacheTTL); err != nil {
		slog.Warn("Failed to set expiration on STM cache", slog.String("user_id", userID), slog.String("error", err.Error()))
	}

	duration := time.Since(start)
	slog.Info("STM cache updated",
		slog.String("user_id", userID),
		slog.Duration("duration", duration),
		slog.Int("event_count", len(events)),
	)

	return nil
}

// ClearSTMContext clears the STM cache for a user.
func (s *STMCache) ClearSTMContext(ctx context.Context, tenantID, userID, agentID string) error {
	cacheKey := s.generateSTMKeyWithScope(tenantID, userID, agentID)
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

// GenerateSTMKey creates the Redis key for a user's STM cache with tenant/agent scope.
// This is a package-level function so both STMCache and Worker use the same key format.
func GenerateSTMKey(tenantID, userID, agentID string) string {
	StmCacheKeyPrefix := os.Getenv("STM_CACHE_KEY_PREFIX")
	if StmCacheKeyPrefix == "" {
		StmCacheKeyPrefix = "stm_cache" // Set default if empty
	}
	return fmt.Sprintf("%s:v1:%s:%s:%s:user_%s", StmCacheKeyPrefix, tenantID, userID, agentID, userID)
}

// generateSTMKeyWithScope delegates to the package-level GenerateSTMKey.
func (s *STMCache) generateSTMKeyWithScope(tenantID, userID, agentID string) string {
	return GenerateSTMKey(tenantID, userID, agentID)
}

// GetCacheStats returns statistics about the STM cache for a user
func (s *STMCache) GetCacheStats(ctx context.Context, tenantID, userID, agentID string) (map[string]interface{}, error) {
	cacheKey := s.generateSTMKeyWithScope(tenantID, userID, agentID)

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

	StmCacheMaxTurns := int64(parseIntEnv("STM_CACHE_MAX_TURNS", 10))
	StmCacheTTL := parseDurationEnv("STM_CACHE_TTL", 2)

	return map[string]interface{}{
		"exists":    true,
		"length":    length,
		"max_turns": StmCacheMaxTurns,
		"ttl_hours": StmCacheTTL.Hours(),
	}, nil
}
