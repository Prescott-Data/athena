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
	LLen(key string) (int64, error)
	Expire(key string, expiration time.Duration) error
	SetEX(key string, value string, expiration time.Duration) error
	Keys(pattern string) ([]string, error)
	BRPop(timeout time.Duration, keys ...string) ([]string, error)
	RPop(key string) (string, error)
	LIndex(key string, index int64) (string, error)
	LSet(key string, index int64, value interface{}) error
}
