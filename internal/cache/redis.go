package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

// RedisClient implements the cache Interface using Redis
type RedisClient struct {
	client *redis.Client
	ctx    context.Context
	ttl    time.Duration
}

// NewRedisClient creates a new Redis client instance
func NewRedisClient() (*RedisClient, error) {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	password := os.Getenv("REDIS_PASSWORD")
	dbStr := os.Getenv("REDIS_DB")
	if dbStr == "" {
		dbStr = "0"
	}
	poolSizeStr := os.Getenv("REDIS_POOL_SIZE")
	if poolSizeStr == "" {
		poolSizeStr = "10"
	}
	poolTimeoutStr := os.Getenv("REDIS_POOL_TIMEOUT")
	if poolTimeoutStr == "" {
		poolTimeoutStr = "30"
	}
	cacheTTLStr := os.Getenv("CACHE_TTL")
	if cacheTTLStr == "" {
		cacheTTLStr = "3600"
	}

	db, err := strconv.Atoi(dbStr)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_DB value: %w", err)
	}

	poolSize, err := strconv.Atoi(poolSizeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_POOL_SIZE value: %w", err)
	}

	poolTimeout, err := strconv.Atoi(poolTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_POOL_TIMEOUT value: %w", err)
	}

	cacheTTL, err := strconv.Atoi(cacheTTLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CACHE_TTL value: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", host, port),
		Password:    password,
		DB:          db,
		PoolSize:    poolSize,
		PoolTimeout: time.Duration(poolTimeout) * time.Second,
	})

	ctx := context.Background()

	// Test the connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisClient{
		client: rdb,
		ctx:    ctx,
		ttl:    time.Duration(cacheTTL) * time.Hour,
	}, nil
}

// Set stores a value in the cache with default TTL
func (r *RedisClient) Set(key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	return r.client.Set(r.ctx, key, data, r.ttl).Err()
}

// SetWithTTL stores a value in the cache with specified TTL
func (r *RedisClient) SetWithTTL(key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	return r.client.Set(r.ctx, key, data, ttl).Err()
}

// Get retrieves a value from the cache
func (r *RedisClient) Get(key string, dest interface{}) error {
	data, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return ErrCacheMiss
		}
		return fmt.Errorf("failed to get value: %w", err)
	}

	if err := json.Unmarshal([]byte(data), dest); err != nil {
		return fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return nil
}

// Delete removes a key from the cache
func (r *RedisClient) Delete(key string) error {
	return r.client.Del(r.ctx, key).Err()
}

// DeletePattern removes all keys matching a pattern
func (r *RedisClient) DeletePattern(pattern string) error {
	keys, err := r.client.Keys(r.ctx, pattern).Result()
	if err != nil {
		return fmt.Errorf("failed to get keys for pattern %s: %w", pattern, err)
	}

	if len(keys) > 0 {
		return r.client.Del(r.ctx, keys...).Err()
	}

	return nil
}

// Exists checks if a key exists in the cache
func (r *RedisClient) Exists(key string) (bool, error) {
	result, err := r.client.Exists(r.ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}
	return result > 0, nil
}

// Health checks the health of the Redis connection
func (r *RedisClient) Health() error {
	return r.client.Ping(r.ctx).Err()
}

// Close closes the Redis connection
func (r *RedisClient) Close() error {
	return r.client.Close()
}

// LRange returns a range of elements from a list
func (r *RedisClient) LRange(key string, start, stop int64) ([]string, error) {
	return r.client.LRange(r.ctx, key, start, stop).Result()
}

// LPush prepends values to a list
func (r *RedisClient) LPush(key string, values ...interface{}) error {
	return r.client.LPush(r.ctx, key, values...).Err()
}

// LTrim trims a list to the specified range
func (r *RedisClient) LTrim(key string, start, stop int64) error {
	return r.client.LTrim(r.ctx, key, start, stop).Err()
}

// LLen returns the length of a list
func (r *RedisClient) LLen(key string) (int64, error) {
	return r.client.LLen(r.ctx, key).Result()
}

// Expire sets a timeout on a key
func (r *RedisClient) Expire(key string, expiration time.Duration) error {
	return r.client.Expire(r.ctx, key, expiration).Err()
}

// SetEX sets a key with expiration
func (r *RedisClient) SetEX(key string, value string, expiration time.Duration) error {
	return r.client.SetEx(r.ctx, key, value, expiration).Err()
}

// Keys returns all keys matching a pattern
func (r *RedisClient) Keys(pattern string) ([]string, error) {
	return r.client.Keys(r.ctx, pattern).Result()
}

// BRPop blocks until a list has elements, then pops from the right
func (r *RedisClient) BRPop(timeout time.Duration, keys ...string) ([]string, error) {
	result, err := r.client.BRPop(r.ctx, timeout, keys...).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // No elements available
		}
		return nil, err
	}
	return result, nil
}

// RPop pops an element from the right of a list
func (r *RedisClient) RPop(key string) (string, error) {
	result, err := r.client.RPop(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return "", err // Return error for cache miss
		}
		return "", err
	}
	return result, nil
}

// LIndex returns the element at index in the list stored at key
func (r *RedisClient) LIndex(key string, index int64) (string, error) {
	result, err := r.client.LIndex(r.ctx, key, index).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil // Return empty string if index out of range or key doesn't exist
		}
		return "", err
	}
	return result, nil
}

// LSet sets the list element at index to value
func (r *RedisClient) LSet(key string, index int64, value interface{}) error {
	// Value needs to be serialized if it's not a string/byte slice
	// but standard redis.LSet expects value.
	// If interface{} is used, we should probably marshal it if it's a struct,
	// but standard go-redis handles basic types.
	// However, STMEvent is a struct, so AddSTMEvent marshals it before calling LPush.
	// If we pass a string (JSON) to LSet, go-redis handles it.
	return r.client.LSet(r.ctx, key, index, value).Err()
}
