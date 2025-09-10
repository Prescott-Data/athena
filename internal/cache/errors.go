package cache

import "errors"

// ErrCacheMiss indicates that the requested key was not found in the cache
var ErrCacheMiss = errors.New("cache miss")

// ErrCacheUnavailable indicates that the cache is not available
var ErrCacheUnavailable = errors.New("cache unavailable")

// ErrInvalidKey indicates that the provided key is invalid
var ErrInvalidKey = errors.New("invalid cache key")

// ErrSerializationFailed indicates that value serialization failed
var ErrSerializationFailed = errors.New("serialization failed")

// ErrDeserializationFailed indicates that value deserialization failed
var ErrDeserializationFailed = errors.New("deserialization failed")
