package cache

import "time"

// Interface defines the contract for cache implementations
type Interface interface {
	Set(key string, value interface{}) error
	SetWithTTL(key string, value interface{}, ttl time.Duration) error
	Get(key string, dest interface{}) error
	Delete(key string) error
	DeletePattern(pattern string) error
	Exists(key string) (bool, error)
	Health() error
	Close() error
	LRange(key string, start, stop int64) ([]string, error)
	LPush(key string, values ...interface{}) error
	LTrim(key string, start, stop int64) error
	// LRem removes up to count occurrences of value from a list (0 removes all).
	LRem(key string, count int64, value interface{}) error
	LLen(key string) (int64, error)
	Expire(key string, expiration time.Duration) error
	SetEX(key string, value string, expiration time.Duration) error
	Keys(pattern string) ([]string, error)
	BRPop(timeout time.Duration, keys ...string) ([]string, error)
	RPop(key string) (string, error)
	LIndex(key string, index int64) (string, error)
	LSet(key string, index int64, value interface{}) error
	HSet(key, field, value string) error
	HGetAll(key string) (map[string]string, error)
	HDel(key string, fields ...string) error
	// SetNX sets key to value with a TTL only if it does not exist; true if it was set.
	SetNX(key, value string, ttl time.Duration) (bool, error)
	// DeleteIfEquals deletes key only if it still holds value (a lock its holder releases).
	DeleteIfEquals(key, value string) error
}
