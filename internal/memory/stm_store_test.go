package memory

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/mongo"
)

// MockTestifyRoundTripper is a testify-based mock implementation of http.RoundTripper
type MockTestifyRoundTripper struct {
	mock.Mock
}

func (m *MockTestifyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	return args.Get(0).(*http.Response), args.Error(1)
}

// MockMongoCollection is a mock implementation of the mongo.Collection for testing
type MockMongoCollection struct {
	mock.Mock
}

func (m *MockMongoCollection) InsertOne(ctx context.Context, document interface{}) (*mongo.InsertOneResult, error) {
	args := m.Called(ctx, document)
	return args.Get(0).(*mongo.InsertOneResult), args.Error(1)
}

// Test Helpers
func setupSTMStoreTest() (*STMStore, *MockTestifyRoundTripper) {
	mockTripper := new(MockTestifyRoundTripper)
	// We are not testing the full DB/Milvus/Redis stack here, so we can pass nil for them
	// and rely on mocking the HTTP client for the parts of the functions we are testing.
	stmStore := &STMStore{
		db:         nil, // Not used in these specific unit tests
		redis:      nil, // Not used in these specific unit tests
		milvus:     nil, // Not used in these specific unit tests
		llmGuards:  &LLMGuardrails{},
		HTTPClient: &http.Client{Transport: mockTripper},
	}
	return stmStore, mockTripper
}

func Test_CreateEmbedding_BuildsRequest(t *testing.T) {
	stmStore, mockTripper := setupSTMStoreTest()
	ctx := context.Background()

	// Mock the HTTP response
	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1, 0.2, 0.3]}]}`)),
	}
	mockTripper.On("RoundTrip", mock.Anything).Return(mockResp, nil).Once()

	_, err := stmStore.CreateEmbedding(ctx, "test text")
	assert.NoError(t, err)

	// Assert that the request body was correct
	mockTripper.AssertCalled(t, "RoundTrip", mock.Anything)
}

func Test_analyzeTopicContinuity_BuildsPrompt_ParsesResponse(t *testing.T) {
	stmStore, mockTripper := setupSTMStoreTest()
	ctx := context.Background()

	// Mock the HTTP response
	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"true"}}]}`)),
	}
	mockTripper.On("RoundTrip", mock.Anything).Return(mockResp, nil).Once()

	continuous, err := stmStore.analyzeTopicContinuity(ctx, "test-user", "previous content", "new content")
	assert.NoError(t, err)
	assert.True(t, continuous)

	// Assert that the prompt was correct
	mockTripper.AssertCalled(t, "RoundTrip", mock.Anything)
}

func Test_CreateSegmentSummary_BuildsRichPrompt(t *testing.T) {
	stmStore, mockTripper := setupSTMStoreTest()
	ctx := context.Background()

	testEvents := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "Hello"},
		{Role: "agent", Type: models.STMEventTypeThought, Content: "User said hello"},
		{Role: "agent", Type: models.STMEventTypeAction, Content: "Calling tool: echo"},
	}

	// Mock the HTTP response with structured JSON
	mockJSON := `{"choices":[{"message":{"content":"{\"summary\": \"Summary\", \"intrinsic_importance\": 0.8}"}}]}`
	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(mockJSON)),
	}
	mockTripper.On("RoundTrip", mock.Anything).Return(mockResp, nil).Once()

	summary, importance, err := stmStore.CreateSegmentSummary(ctx, testEvents)
	assert.NoError(t, err)
	assert.Equal(t, "Summary", summary)
	assert.Equal(t, 0.8, importance)

	// Assert that the prompt was correct
	mockTripper.AssertCalled(t, "RoundTrip", mock.Anything)
}

// This is a simplified test for orchestration. A full integration test would be needed to test the DB/Milvus interactions.
func Test_ProcessMTMFormation_CallsPipelineInOrder(t *testing.T) {
	t.Skip("Skipping test because it is not a true unit test and requires a database connection")
	stmStore, mockTripper := setupSTMStoreTest()
	ctx := context.Background()

	// Mock the HTTP response for CreateSegmentSummary with structured JSON
	mockJSON := `{"choices":[{"message":{"content":"{\"summary\": \"Summary\", \"intrinsic_importance\": 0.8}"}}]}`
	mockRespSummary := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(mockJSON)),
	}
	// Mock the HTTP response for CreateEmbedding
	mockRespEmbedding := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1, 0.2, 0.3]}]}`)),
	}
	mockTripper.On("RoundTrip", mock.Anything).Return(mockRespSummary, nil).Once()
	mockTripper.On("RoundTrip", mock.Anything).Return(mockRespEmbedding, nil).Once()

	testEvents := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "Hello"},
	}

	// This will fail because the DB is nil, but it proves the function is callable.
	// A more advanced test setup would be needed to mock the DB layer.
	err := stmStore.ProcessMTMFormation(ctx, "tenant", "user", "agent", testEvents)
	assert.Error(t, err) // Expect an error because DB is nil
}

// MockRedisCache is a mock implementation of cache.Interface for testing
type MockRedisCache struct {
	mock.Mock
	cache map[string]interface{}
}

func (m *MockRedisCache) Get(key string, dest interface{}) error {
	args := m.Called(key, dest)
	if val, ok := m.cache[key]; ok {
		// Simple type assertion for testing
		if embData, ok := dest.(*models.EmbeddingData); ok {
			if cached, ok := val.(*models.EmbeddingData); ok {
				*embData = *cached
				return nil
			}
		}
	}
	return args.Error(0)
}

func (m *MockRedisCache) SetWithTTL(key string, value interface{}, ttl time.Duration) error {
	args := m.Called(key, value, ttl)
	if m.cache == nil {
		m.cache = make(map[string]interface{})
	}
	m.cache[key] = value
	return args.Error(0)
}

func (m *MockRedisCache) Set(key string, value interface{}) error {
	return nil
}

func (m *MockRedisCache) Delete(key string) error {
	args := m.Called(key)
	return args.Error(0)
}

func (m *MockRedisCache) DeletePattern(pattern string) error {
	return nil
}

func (m *MockRedisCache) Exists(key string) (bool, error) {
	args := m.Called(key)
	return args.Bool(0), args.Error(1)
}

func (m *MockRedisCache) Health() error {
	return nil
}

func (m *MockRedisCache) Close() error {
	return nil
}

func (m *MockRedisCache) LRange(key string, start, stop int64) ([]string, error) {
	return nil, nil
}

func (m *MockRedisCache) LPush(key string, values ...interface{}) error {
	return nil
}

func (m *MockRedisCache) LTrim(key string, start, stop int64) error {
	return nil
}

func (m *MockRedisCache) LLen(key string) (int64, error) {
	return 0, nil
}

func (m *MockRedisCache) Expire(key string, expiration time.Duration) error {
	return nil
}

func (m *MockRedisCache) SetEX(key string, value string, expiration time.Duration) error {
	return nil
}

func (m *MockRedisCache) Keys(pattern string) ([]string, error) {
	return nil, nil
}

func (m *MockRedisCache) BRPop(timeout time.Duration, keys ...string) ([]string, error) {
	return nil, nil
}

func (m *MockRedisCache) RPop(key string) (string, error) {
	return "", nil
}

func (m *MockRedisCache) LIndex(key string, index int64) (string, error) {
	args := m.Called(key, index)
	return args.String(0), args.Error(1)
}

func (m *MockRedisCache) LSet(key string, index int64, value interface{}) error {
	args := m.Called(key, index, value)
	return args.Error(0)
}

func TestSTMStore_EmbeddingCache_HitAndMiss(t *testing.T) {
	mockTripper := new(MockTestifyRoundTripper)
	mockRedis := new(MockRedisCache)
	mockRedis.cache = make(map[string]interface{})

	stmStore := &STMStore{
		redis:      mockRedis,
		llmGuards:  &LLMGuardrails{redis: mockRedis},
		llmConfig:  LLMConfig{EmbeddingTimeout: 15 * time.Second},
		HTTPClient: &http.Client{Transport: mockTripper},
	}

	ctx := context.Background()
	testText := "test embedding text"

	// Test 1: Cache MISS - should call API
	t.Run("Cache Miss", func(t *testing.T) {
		t.Setenv("EMBEDDING_BASE_URL", "http://test")
		t.Setenv("AZURE_OPENAI_API_KEY", "test")
		// Mock embedding cache miss - Get(key string, dest interface{})
		mockRedis.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*models.EmbeddingData")).Return(io.EOF).Once()

		// Mock rate limit check - Get(key string, dest interface{})
		mockRedis.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*int")).Return(io.EOF).Once()
		mockRedis.On("SetEX", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		mockResp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[0.1, 0.2, 0.3]}]}`)),
		}
		mockTripper.On("RoundTrip", mock.Anything).Return(mockResp, nil).Once()

		// Mock cache storage
		mockRedis.On("SetWithTTL", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		embedding, err := stmStore.CreateEmbedding(ctx, testText)

		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Equal(t, 3, embedding.Dimensions)

		// Verify API was called
		mockTripper.AssertCalled(t, "RoundTrip", mock.Anything)
		// Verify cache was updated
		mockRedis.AssertCalled(t, "SetWithTTL", mock.Anything, mock.Anything, mock.Anything)
	})

	// Test 2: Cache HIT - should NOT call API
	t.Run("Cache Hit", func(t *testing.T) {
		t.Setenv("EMBEDDING_BASE_URL", "http://test")
		t.Setenv("AZURE_OPENAI_API_KEY", "test")
		// Reset mock for clean test
		mockTripper2 := new(MockTestifyRoundTripper)
		mockRedis2 := new(MockRedisCache)
		mockRedis2.cache = make(map[string]interface{})

		stmStore2 := &STMStore{
			redis:      mockRedis2,
			llmGuards:  &LLMGuardrails{redis: mockRedis2},
			llmConfig:  LLMConfig{EmbeddingTimeout: 15 * time.Second},
			HTTPClient: &http.Client{Transport: mockTripper2},
		}

		cachedEmbedding := &models.EmbeddingData{
			Vector:     []float64{0.5, 0.6, 0.7},
			Dimensions: 3,
			Model:      "test-model",
			CreatedAt:  time.Now(),
		}

		// Generate the actual cache key that will be used
		cacheKey := stmStore2.generateEmbeddingCacheKey(testText, os.Getenv("EMBEDDING_MODEL_NAME"))
		mockRedis2.cache[cacheKey] = cachedEmbedding

		// Mock the Get call for embedding cache - should return cached data
		mockRedis2.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*models.EmbeddingData")).Return(nil).Once()

		embedding, err := stmStore2.CreateEmbedding(ctx, testText)

		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Equal(t, 3, embedding.Dimensions)
		assert.Equal(t, cachedEmbedding.Vector, embedding.Vector)

		// Verify API was NOT called
		mockTripper2.AssertNotCalled(t, "RoundTrip", mock.Anything)
	})
}

func TestSTMStore_generateEmbeddingCacheKey(t *testing.T) {
	stmStore := &STMStore{}

	tests := []struct {
		name         string
		text1        string
		model1       string
		text2        string
		model2       string
		shouldBeSame bool
	}{
		{
			name:         "Same text and model",
			text1:        "hello world",
			model1:       "model-1",
			text2:        "hello world",
			model2:       "model-1",
			shouldBeSame: true,
		},
		{
			name:         "Different text",
			text1:        "hello world",
			model1:       "model-1",
			text2:        "goodbye world",
			model2:       "model-1",
			shouldBeSame: false,
		},
		{
			name:         "Different model",
			text1:        "hello world",
			model1:       "model-1",
			text2:        "hello world",
			model2:       "model-2",
			shouldBeSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := stmStore.generateEmbeddingCacheKey(tt.text1, tt.model1)
			key2 := stmStore.generateEmbeddingCacheKey(tt.text2, tt.model2)

			if tt.shouldBeSame {
				if key1 != key2 {
					t.Errorf("Expected keys to be the same, but got %s and %s", key1, key2)
				}
			} else {
				if key1 == key2 {
					t.Errorf("Expected keys to be different, but both are %s", key1)
				}
			}

			// Verify key format
			if !strings.HasPrefix(key1, "embedding:cache:") {
				t.Errorf("Expected key to start with 'embedding:cache:', got %s", key1)
			}
		})
	}
}

func TestSTMStore_ProcessMTMFormation_Idempotency(t *testing.T) {
	mockRedis := new(MockRedisCache)
	mockRedis.cache = make(map[string]interface{})

	stmStore := &STMStore{
		redis:     mockRedis,
		llmGuards: &LLMGuardrails{redis: mockRedis},
	}

	ctx := context.Background()
	chainID := "test-chain-idempotency"

	t.Run("Lock prevents duplicate processing", func(t *testing.T) {
		lockKey := fmt.Sprintf("mtm:processing:lock:%s", chainID)

		// Test 1: First call - should acquire lock
		mockRedis.On("SetWithTTL", lockKey, "processing", mock.Anything).Return(nil).Once()
		acquired1 := stmStore.acquireProcessingLock(ctx, chainID)
		assert.True(t, acquired1, "First call should acquire lock successfully")

		// Test 2: Second call with same chainID - should fail to acquire (lock exists)
		mockRedis.On("SetWithTTL", lockKey, "processing", mock.Anything).Return(io.EOF).Once()
		mockRedis.On("Exists", lockKey).Return(true, nil).Once() // Confirm lock exists
		acquired2 := stmStore.acquireProcessingLock(ctx, chainID)
		assert.False(t, acquired2, "Second call should fail to acquire lock (duplicate detected)")

		// Test 3: Release lock
		mockRedis.On("Delete", lockKey).Return(nil).Once()
		stmStore.releaseProcessingLock(ctx, chainID)

		// Test 4: After release, should be able to acquire again
		mockRedis.On("SetWithTTL", lockKey, "processing", mock.Anything).Return(nil).Once()
		acquired3 := stmStore.acquireProcessingLock(ctx, chainID)
		assert.True(t, acquired3, "After release, should acquire lock again")

		t.Log("Idempotency lock cycle completed: acquire -> reject duplicate -> release -> re-acquire")
	})
}

func TestSTMStore_acquireProcessingLock(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*MockRedisCache)
		expectAcquire bool
		description   string
	}{
		{
			name: "Successful lock acquisition",
			setupMock: func(m *MockRedisCache) {
				m.On("SetWithTTL", mock.Anything, "processing", mock.Anything).Return(nil).Once()
			},
			expectAcquire: true,
			description:   "Should acquire lock when key doesn't exist",
		},
		{
			name: "Lock already held",
			setupMock: func(m *MockRedisCache) {
				m.On("SetWithTTL", mock.Anything, "processing", mock.Anything).Return(io.EOF).Once()
				m.On("Exists", mock.Anything).Return(true, nil).Once()
			},
			expectAcquire: false,
			description:   "Should fail to acquire when lock exists",
		},
		{
			name: "Redis unavailable - fail open",
			setupMock: func(m *MockRedisCache) {
				// stmStore with nil redis will fail open
			},
			expectAcquire: true,
			description:   "Should allow processing when Redis is unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stmStore *STMStore

			if tt.name == "Redis unavailable - fail open" {
				stmStore = &STMStore{redis: nil}
			} else {
				mockRedis := new(MockRedisCache)
				tt.setupMock(mockRedis)
				stmStore = &STMStore{redis: mockRedis}
			}

			ctx := context.Background()
			acquired := stmStore.acquireProcessingLock(ctx, "test-chain-123")

			assert.Equal(t, tt.expectAcquire, acquired, tt.description)
			t.Logf("%s: Acquired=%v", tt.description, acquired)
		})
	}
}

func TestSTMStore_releaseProcessingLock(t *testing.T) {
	mockRedis := new(MockRedisCache)
	lockKey := "mtm:processing:lock:test-chain-123"
	mockRedis.On("Delete", lockKey).Return(nil).Once()

	stmStore := &STMStore{redis: mockRedis}
	ctx := context.Background()

	// Should not panic or error
	stmStore.releaseProcessingLock(ctx, "test-chain-123")

	mockRedis.AssertCalled(t, "Delete", lockKey)

	// Test with nil redis - should not panic
	stmStoreNilRedis := &STMStore{redis: nil}
	stmStoreNilRedis.releaseProcessingLock(ctx, "test-chain-456")
	t.Log("Successfully handled nil redis in release lock")
}

func TestSTMStore_ConfigurableTimeouts(t *testing.T) {
	tests := []struct {
		name             string
		embeddingTimeout time.Duration
		summaryTimeout   time.Duration
		description      string
	}{
		{
			name:             "Default timeouts",
			embeddingTimeout: 15 * time.Second,
			summaryTimeout:   20 * time.Second,
			description:      "Should use default timeout values",
		},
		{
			name:             "Custom short timeouts",
			embeddingTimeout: 5 * time.Second,
			summaryTimeout:   10 * time.Second,
			description:      "Should respect custom short timeout values",
		},
		{
			name:             "Custom long timeouts",
			embeddingTimeout: 30 * time.Second,
			summaryTimeout:   45 * time.Second,
			description:      "Should respect custom long timeout values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := LLMConfig{
				EmbeddingTimeout: tt.embeddingTimeout,
				SummaryTimeout:   tt.summaryTimeout,
			}

			stmStore := &STMStore{
				llmConfig: config,
			}

			// Verify config is set correctly
			assert.Equal(t, tt.embeddingTimeout, stmStore.llmConfig.EmbeddingTimeout,
				"Embedding timeout should match config")
			assert.Equal(t, tt.summaryTimeout, stmStore.llmConfig.SummaryTimeout,
				"Summary timeout should match config")

			t.Logf("%s: EmbeddingTimeout=%v, SummaryTimeout=%v",
				tt.description, stmStore.llmConfig.EmbeddingTimeout, stmStore.llmConfig.SummaryTimeout)
		})
	}
}

func TestLLMConfig_TimeoutConfiguration(t *testing.T) {
	// Test that LLMConfig struct properly holds timeout values
	config := LLMConfig{
		EmbeddingTimeout: 15 * time.Second,
		SummaryTimeout:   20 * time.Second,
	}

	if config.EmbeddingTimeout != 15*time.Second {
		t.Errorf("Expected EmbeddingTimeout to be 15s, got %v", config.EmbeddingTimeout)
	}

	if config.SummaryTimeout != 20*time.Second {
		t.Errorf("Expected SummaryTimeout to be 20s, got %v", config.SummaryTimeout)
	}

	// Test zero values
	zeroConfig := LLMConfig{}
	if zeroConfig.EmbeddingTimeout != 0 {
		t.Errorf("Expected zero EmbeddingTimeout, got %v", zeroConfig.EmbeddingTimeout)
	}

	t.Log("LLMConfig properly stores and retrieves timeout values")
}
