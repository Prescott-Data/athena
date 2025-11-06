package memory

import (
	"encoding/json"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockRedis is a mock implementation of the cache.Interface for testing
type MockRedis struct {
	mock.Mock
}

func (m *MockRedis) Get(key string, dest interface{}) error {
	args := m.Called(key, dest)
	// Simulate unmarshalling by taking the string and unmarshalling it into dest
	if val, ok := args.Get(0).(string); ok {
		return json.Unmarshal([]byte(val), dest)
	}
	return args.Error(1)
}

func (m *MockRedis) Set(key string, value interface{}) error {
	args := m.Called(key, value)
	return args.Error(0)
}

func (m *MockRedis) SetWithTTL(key string, value interface{}, ttl time.Duration) error {
	args := m.Called(key, value, ttl)
	return args.Error(0)
}

func (m *MockRedis) SetEX(key string, value string, expiration time.Duration) error {
	args := m.Called(key, value, expiration)
	return args.Error(0)
}

func (m *MockRedis) LPush(key string, values ...interface{}) error {
	args := m.Called(key, values)
	return args.Error(0)
}

func (m *MockRedis) LRange(key string, start, stop int64) ([]string, error) {
	args := m.Called(key, start, stop)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRedis) LTrim(key string, start, stop int64) error {
	args := m.Called(key, start, stop)
	return args.Error(0)
}

func (m *MockRedis) Expire(key string, expiration time.Duration) error {
	args := m.Called(key, expiration)
	return args.Error(0)
}

func (m *MockRedis) Delete(key string) error {
	args := m.Called(key)
	return args.Error(0)
}

func (m *MockRedis) DeletePattern(pattern string) error {
	args := m.Called(pattern)
	return args.Error(0)
}

func (m_ *MockRedis) Exists(key string) (bool, error) {
	args := m_.Called(key)
	return args.Bool(0), args.Error(1)
}

func (m *MockRedis) LLen(key string) (int64, error) {
	args := m.Called(key)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRedis) Keys(pattern string) ([]string, error) {
	args := m.Called(pattern)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRedis) BRPop(timeout time.Duration, keys ...string) ([]string, error) {
	args := m.Called(timeout, keys)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRedis) Health() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRedis) Close() error {
	args := m.Called()
	return args.Error(0)
}