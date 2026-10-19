package memory

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/llm"
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

// MockLLMProvider is a testify-based mock implementation of llm.Provider
type MockLLMProvider struct {
	mock.Mock
}

func (m *MockLLMProvider) GenerateCompletion(ctx context.Context, req llm.CompletionRequest) (string, error) {
	args := m.Called(ctx, req)
	return args.String(0), args.Error(1)
}

func (m *MockLLMProvider) CreateEmbedding(ctx context.Context, req llm.EmbeddingRequest) ([]float64, error) {
	args := m.Called(ctx, req)
	return args.Get(0).([]float64), args.Error(1)
}

func (m *MockLLMProvider) EmbeddingModel() string {
	return "test-embedding-model"
}

// newTestStoreWithMockClient returns an STMStore whose LLM provider posts to
// the TestMain mock URLs through the given HTTP client. Used by tests that
// stub LLM traffic at the transport level.
func newTestStoreWithMockClient(client *http.Client) *STMStore {
	store := NewSTMStore(nil, nil, nil)
	provider := llm.NewAzureProvider(os.Getenv("LLM_BASE_URL"), "test-key", os.Getenv("EMBEDDING_BASE_URL"), "test-embedding")
	if client != nil {
		provider.Client = client
	}
	store.llmProvider = provider
	return store
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
func setupSTMStoreTest() (*STMStore, *MockLLMProvider) {
	mockProvider := new(MockLLMProvider)
	// We are not testing the full DB/Milvus/Redis stack here, so we can pass nil for them
	// and rely on mocking the LLM provider for the parts of the functions we are testing.
	stmStore := &STMStore{
		db:          nil, // Not used in these specific unit tests
		redis:       nil, // Not used in these specific unit tests
		milvus:      nil, // Not used in these specific unit tests
		llmGuards:   &LLMGuardrails{},
		llmConfig:   LLMConfig{EmbeddingTimeout: 15 * time.Second, SummaryTimeout: 20 * time.Second},
		llmProvider: mockProvider,
	}
	return stmStore, mockProvider
}

func Test_CreateEmbedding_BuildsRequest(t *testing.T) {
	stmStore, mockProvider := setupSTMStoreTest()
	ctx := context.Background()

	mockProvider.On("CreateEmbedding", mock.Anything, llm.EmbeddingRequest{Input: "test text"}).
		Return([]float64{0.1, 0.2, 0.3}, nil).Once()

	_, err := stmStore.CreateEmbedding(ctx, "test text")
	assert.NoError(t, err)

	mockProvider.AssertExpectations(t)
}

func Test_analyzeTopicContinuity_BuildsPrompt_ParsesResponse(t *testing.T) {
	stmStore, mockProvider := setupSTMStoreTest()
	ctx := context.Background()

	mockProvider.On("GenerateCompletion", mock.Anything, mock.MatchedBy(func(req llm.CompletionRequest) bool {
		return strings.Contains(req.Prompt, "previous content") && strings.Contains(req.Prompt, "new content")
	})).Return("true", nil).Once()

	continuous, err := stmStore.analyzeTopicContinuity(ctx, "test-user", "previous content", "new content")
	assert.NoError(t, err)
	assert.True(t, continuous)

	mockProvider.AssertExpectations(t)
}

func Test_CreateSegmentSummary_BuildsRichPrompt(t *testing.T) {
	stmStore, mockProvider := setupSTMStoreTest()
	ctx := context.Background()

	testEvents := []models.CognitiveEvent{
		{Role: "user", Type: models.STMEventTypeMessage, Content: "Hello"},
		{Role: "agent", Type: models.STMEventTypeThought, Content: "User said hello"},
		{Role: "agent", Type: models.STMEventTypeAction, Content: "Calling tool: echo"},
	}

	mockProvider.On("GenerateCompletion", mock.Anything, mock.MatchedBy(func(req llm.CompletionRequest) bool {
		return req.JSONSchema != nil && strings.Contains(req.Prompt, "Calling tool: echo")
	})).Return(`{"summary": "Summary", "intrinsic_importance": 0.8}`, nil).Once()

	summary, importance, err := stmStore.CreateSegmentSummary(ctx, testEvents)
	assert.NoError(t, err)
	assert.Equal(t, "Summary", summary)
	assert.Equal(t, 0.8, importance)

	mockProvider.AssertExpectations(t)
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
	mockProvider := new(MockLLMProvider)
	mockRedis := new(MockRedisCache)
	mockRedis.cache = make(map[string]interface{})

	stmStore := &STMStore{
		redis:       mockRedis,
		llmGuards:   &LLMGuardrails{redis: mockRedis},
		llmConfig:   LLMConfig{EmbeddingTimeout: 15 * time.Second},
		llmProvider: mockProvider,
	}

	ctx := context.Background()
	testText := "test embedding text"

	// Test 1: Cache MISS - should call the provider
	t.Run("Cache Miss", func(t *testing.T) {
		// Mock embedding cache miss - Get(key string, dest interface{})
		mockRedis.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*models.EmbeddingData")).Return(io.EOF).Once()

		// Mock rate limit check - Get(key string, dest interface{})
		mockRedis.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*int")).Return(io.EOF).Once()
		mockRedis.On("SetEX", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		mockProvider.On("CreateEmbedding", mock.Anything, llm.EmbeddingRequest{Input: testText}).
			Return([]float64{0.1, 0.2, 0.3}, nil).Once()

		// Mock cache storage
		mockRedis.On("SetWithTTL", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		embedding, err := stmStore.CreateEmbedding(ctx, testText)

		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Equal(t, 3, embedding.Dimensions)

		// Verify the provider was called
		mockProvider.AssertExpectations(t)
		// Verify cache was updated
		mockRedis.AssertCalled(t, "SetWithTTL", mock.Anything, mock.Anything, mock.Anything)
	})

	// Test 2: Cache HIT - should NOT call the provider
	t.Run("Cache Hit", func(t *testing.T) {
		// Reset mock for clean test
		mockProvider2 := new(MockLLMProvider)
		mockRedis2 := new(MockRedisCache)
		mockRedis2.cache = make(map[string]interface{})

		stmStore2 := &STMStore{
			redis:       mockRedis2,
			llmGuards:   &LLMGuardrails{redis: mockRedis2},
			llmConfig:   LLMConfig{EmbeddingTimeout: 15 * time.Second},
			llmProvider: mockProvider2,
		}

		cachedEmbedding := &models.EmbeddingData{
			Vector:     []float64{0.5, 0.6, 0.7},
			Dimensions: 3,
			Model:      "test-model",
			CreatedAt:  time.Now(),
		}

		// Generate the actual cache key that will be used (provider's embedding model)
		cacheKey := stmStore2.generateEmbeddingCacheKey(testText, mockProvider2.EmbeddingModel())
		mockRedis2.cache[cacheKey] = cachedEmbedding

		// Mock the Get call for embedding cache - should return cached data
		mockRedis2.On("Get", mock.AnythingOfType("string"), mock.AnythingOfType("*models.EmbeddingData")).Return(nil).Once()

		embedding, err := stmStore2.CreateEmbedding(ctx, testText)

		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Equal(t, 3, embedding.Dimensions)
		assert.Equal(t, cachedEmbedding.Vector, embedding.Vector)

		// Verify the provider was NOT called
		mockProvider2.AssertNotCalled(t, "CreateEmbedding", mock.Anything, mock.Anything)
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
	stmStore := &STMStore{redis: mockRedis, llmGuards: &LLMGuardrails{redis: mockRedis}}
	ctx := context.Background()
	lockKey := "mtm:processing:lock:test-chain-idempotency"

	// The lock is taken with SET NX and a holder token.
	mockRedis.On("SetNX", lockKey, mock.Anything, 5*time.Minute).Return(true, nil).Once()
	release := stmStore.acquireProcessingLock(ctx, "test-chain-idempotency")
	require.NotNil(t, release, "the first caller holds the lock")

	// While held, SET NX fails and the second caller does not process the chain.
	mockRedis.On("SetNX", lockKey, mock.Anything, 5*time.Minute).Return(false, nil).Once()
	assert.Nil(t, stmStore.acquireProcessingLock(ctx, "test-chain-idempotency"))

	// Release deletes the key only if this holder still holds it.
	holder := mockRedis.Calls[0].Arguments.String(1)
	mockRedis.On("DeleteIfEquals", lockKey, holder).Return(nil).Once()
	release()
	mockRedis.AssertExpectations(t)
}

func TestSTMStore_acquireProcessingLock_FailsOpenWithoutRedis(t *testing.T) {
	stmStore := &STMStore{redis: nil}
	release := stmStore.acquireProcessingLock(context.Background(), "test-chain-123")
	require.NotNil(t, release)
	release()

	errRedis := new(MockRedisCache)
	errRedis.On("SetNX", mock.Anything, mock.Anything, mock.Anything).Return(false, io.EOF).Once()
	assert.NotNil(t, (&STMStore{redis: errRedis}).acquireProcessingLock(context.Background(), "test-chain-123"),
		"a Redis error proceeds, as before")
}

func TestSTMStore_ProcessingLockIsExclusiveOnRealRedis(t *testing.T) {
	client, err := setupRedisTestClient()
	if err != nil {
		t.Skipf("Skipping: real Redis not available: %v", err)
	}
	stmStore := &STMStore{redis: client}
	ctx := context.Background()
	chainID := "test-chain-exclusive-" + uuid.New().String()

	first := stmStore.acquireProcessingLock(ctx, chainID)
	require.NotNil(t, first)
	assert.Nil(t, stmStore.acquireProcessingLock(ctx, chainID), "a second worker is refused while the first holds it")
	first()
	again := stmStore.acquireProcessingLock(ctx, chainID)
	require.NotNil(t, again, "free again once released")
	again()
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

func (m *MockRedisCache) SetNX(key, value string, ttl time.Duration) (bool, error) {
	args := m.Called(key, value, ttl)
	return args.Bool(0), args.Error(1)
}

func (m *MockRedisCache) DeleteIfEquals(key, value string) error {
	args := m.Called(key, value)
	return args.Error(0)
}
