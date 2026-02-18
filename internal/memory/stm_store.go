package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/llm"
	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

// STMStorer defines the interface for STM store operations.
type STMStorer interface {
	CreateEmbedding(ctx context.Context, textToEmbed string) (*models.EmbeddingData, error)
	analyzeTopicContinuity(ctx context.Context, userID string, previousContent string, newContent string) (bool, error)
	ProcessMTMFormation(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error
	StoreCognitiveEvent(ctx context.Context, event *models.CognitiveEvent) (primitive.ObjectID, error)
	SearchSimilarChains(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]*models.CognitiveChain, error)
}

const (
	// CognitiveEventsCollection is the MongoDB collection name for cognitive events
	CognitiveEventsCollection = "cognitive_events"
	// CognitiveChainsCollection is the MongoDB collection name for cognitive chains
	CognitiveChainsCollection = "cognitive_chains"
)

// parseFloatEnv reads a float from env with default
// This is the "master" copy. Delete duplicates from other files in 'package memory'.
func parseFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// parseIntEnv reads an int from env with default
// This is the "master" copy. Delete duplicates from other files in 'package memory'.
func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// parseBoolEnv reads a bool from env with default
// This is the "master" copy. Delete duplicates from other files in 'package memory'.
func parseBoolEnv(key string, defaultValue bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val == "true"
}

// normalizeVector normalizes a vector to unit length
func normalizeVector(v []float64) []float64 {
	if len(v) == 0 {
		return v
	}
	var norm float64
	for _, val := range v {
		norm += val * val
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v // Return zero vector as-is
	}
	normalized := make([]float64, len(v))
	for i, val := range v {
		normalized[i] = val / norm
	}
	return normalized
}

// cosineSimilarity computes cosine similarity between two vectors
func cosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) || len(a) == 0 {
		return 0, fmt.Errorf("vector mismatch or zero length")
	}
	normA := normalizeVector(a)
	normB := normalizeVector(b)
	var dot float64
	for i := range normA {
		dot += normA[i] * normB[i]
	}
	return dot, nil
}

// LLMGuardrails manages rate limiting and circuit breaker for LLM calls
type LLMGuardrails struct {
	redis                cache.Interface
	circuitBreakerOpen   bool
	circuitBreakerOpened time.Time
	failureCount         int
}

// checkRateLimit checks if user has exceeded LLM call rate limit
func (lg *LLMGuardrails) checkRateLimit(ctx context.Context, userID string) bool {
	_ = ctx // Mark as used to satisfy linter, as cache.Interface doesn't use it
	if lg.redis == nil {
		return true // Allow if Redis unavailable
	}
	llmRateLimit := parseIntEnv("LLM_RATE_LIMIT_PER_MINUTE", 50)
	rateLimitKey := fmt.Sprintf("llm_rate_limit:%s", userID)

	// Get current count
	var count int
	err := lg.redis.Get(rateLimitKey, &count)
	if err != nil {
		// Key doesn't exist or error, start fresh
		count = 0
	}

	if count >= llmRateLimit {
		log.Printf("WARN: LLM rate limit exceeded for user %s: %d/%d calls", userID, count, llmRateLimit)
		return false
	}

	// Increment and set expiration
	newCount := count + 1
	lg.redis.SetEX(rateLimitKey, fmt.Sprintf("%d", newCount), time.Minute)

	return true
}

// checkCircuitBreaker checks if circuit breaker is open
func (lg *LLMGuardrails) checkCircuitBreaker() bool {
	if !lg.circuitBreakerOpen {
		return true
	}

	llmCircuitBreakerTimeout := parseIntEnv("LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS", 60)

	// Check if timeout has passed
	if time.Since(lg.circuitBreakerOpened) > time.Duration(llmCircuitBreakerTimeout)*time.Second {
		lg.circuitBreakerOpen = false
		lg.failureCount = 0
		log.Printf("INFO: LLM circuit breaker reset after timeout")
		return true
	}

	log.Printf("WARN: LLM circuit breaker is open, blocking call")
	return false
}

// recordLLMResult records success/failure for circuit breaker
func (lg *LLMGuardrails) recordLLMResult(success bool) {
	if success {
		// Reset failure count on success
		lg.failureCount = 0
		if lg.circuitBreakerOpen {
			lg.circuitBreakerOpen = false
			log.Printf("INFO: LLM circuit breaker closed after successful call")
		}
	} else {
		llmCircuitBreakerThreshold := parseIntEnv("LLM_CIRCUIT_BREAKER_THRESHOLD", 5)
		lg.failureCount++
		if lg.failureCount >= llmCircuitBreakerThreshold && !lg.circuitBreakerOpen {
			lg.circuitBreakerOpen = true
			lg.circuitBreakerOpened = time.Now()
			log.Printf("WARN: LLM circuit breaker opened after %d failures", lg.failureCount)
		}
	}
}

// --- END LLM GUARDRAILS ---

// LLMConfig holds configuration for LLM client timeouts and settings
type LLMConfig struct {
	EmbeddingTimeout time.Duration
	SummaryTimeout   time.Duration
}

// STMStore manages the Short-Term Memory store operations
type STMStore struct {
	db          *mongo.Database
	redis       cache.Interface
	milvus      *MilvusClient
	llmGuards   *LLMGuardrails
	llmConfig   LLMConfig
	HTTPClient  *http.Client
	llmProvider llm.Provider

	// MTM Components
	qualityValidator  *QualityValidator
	topicAnalyzer     *TopicAnalyzer
	sessionManager    *SessionManager
	parallelProcessor *ParallelProcessor
}

// NewSTMStore creates a new STM store instance
func NewSTMStore(database *mongo.Database, redisClient cache.Interface) *STMStore {
	MilvusHost := os.Getenv("MILVUS_HOST")
	MilvusPort := os.Getenv("MILVUS_PORT")

	var milvusClient *MilvusClient
	if MilvusHost != "" && MilvusPort != "" {
		var err error
		milvusClient, err = NewMilvusClient(MilvusHost, MilvusPort)
		if err != nil {
			log.Printf("WARN: Failed to initialize Milvus client: %v", err)
		} else {
			log.Println("INFO: Milvus client initialized successfully")
		}
	}

	// Load LLM configuration from environment variables
	llmConfig := LLMConfig{
		EmbeddingTimeout: time.Duration(parseIntEnv("LLM_EMBEDDING_TIMEOUT_SEC", 15)) * time.Second,
		SummaryTimeout:   time.Duration(parseIntEnv("LLM_SUMMARY_TIMEOUT_SEC", 20)) * time.Second,
	}

	providerType := os.Getenv("LLM_PROVIDER")
	if providerType == "" {
		providerType = "azure" // Default to azure for backward compatibility
	}

	factory := &llm.Factory{}
	llmProvider, err := factory.NewProvider(providerType)
	if err != nil {
		log.Printf("WARN: Failed to initialize LLM provider: %v", err)
	}

	ss := &STMStore{
		db:          database,
		redis:       redisClient,
		milvus:      milvusClient,
		llmConfig:   llmConfig,
		llmGuards: &LLMGuardrails{
			redis: redisClient,
		},
		HTTPClient:  &http.Client{},
		llmProvider: llmProvider,
	}

	// Initialize MTM components
	ss.qualityValidator = NewQualityValidator()
	ss.parallelProcessor = NewParallelProcessor(10, ss)
	ss.topicAnalyzer = NewTopicAnalyzer(ss, ss.parallelProcessor)
	ss.sessionManager = NewSessionManager(database, ss)

	return ss
}

// ProcessMTMFormation orchestrates the MTM persistence pipeline
// This logic is based on the refactored 'archivist.go'
func (s *STMStore) ProcessMTMFormation(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error {
	if len(events) == 0 {
		log.Println("INFO: ProcessMTMFormation called with no events.")
		return nil
	}

	// Get ChainID from events if set, otherwise generate a new one
	chainID := events[0].ChainID
	if chainID == "" {
		chainID = uuid.New().String()
		log.Printf("INFO: Generated new ChainID %s for MTM formation", chainID)
	}

	// Idempotency check: ensure we don't process the same chain twice
	if !s.acquireProcessingLock(ctx, chainID) {
		log.Printf("INFO: Chain %s is already being processed or has been processed recently. Skipping.", chainID)
		return nil
	}
	defer s.releaseProcessingLock(ctx, chainID)

	log.Printf("INFO: Starting MTM formation for user %s with %d events.", userID, len(events))

	// Step 1: (Analysis)
	// We create the summary *first* to build the candidate.
	summary, intrinsicImportance, err := s.CreateSegmentSummary(ctx, events)
	if err != nil {
		// Log but don't fail, we can use a placeholder
		log.Printf("WARN: Failed to create segment summary for chain %s: %v", chainID, err)
		summary = "Conversation segment."
		intrinsicImportance = 0.5
	}
	log.Printf("INFO: Topic analysis complete. Main topic: %s (Importance: %.2f)", summary, intrinsicImportance)

	// Step 2: (Build Candidate)
	candidateChain := &models.CognitiveChain{
		ID:                  primitive.NewObjectID(), // Generate a temp ID
		TenantID:            tenantID,
		UserID:              userID,
		AgentID:             agentID,
		ChainID:             chainID,
		Topic:               "<placeholder>", // Placeholder, TopicAnalyzer can fill this
		Summary:             summary,
		StartedAt:           events[0].CreatedAt,
		LastEventAt:         events[len(events)-1].CreatedAt,
		EventCount:          len(events),
		Status:              "pending",           // Not yet saved
		RecallStrength:      1.0,                 // Initialize Ebbinghaus recall strength
		IntrinsicImportance: intrinsicImportance, // From LLM analysis
	}

	// Step 3: (Quality Gate)
	validationResult, err := s.qualityValidator.ValidateSegment(ctx, candidateChain, events)
	if err != nil {
		return fmt.Errorf("failed during quality validation: %w", err)
	}
	if !validationResult.ShouldStore {
		log.Printf("INFO: MTM formation discarded for chain %s due to low quality. Score: %.2f.",
			chainID, validationResult.QualityScore)
		// Here you would delete the "in_stm" events from Mongo
		// Or just let them expire from STM cache (if you're not saving to Mongo first)
		return nil
	}
	log.Printf("INFO: Quality gate passed for chain %s. Score: %.2f", chainID, validationResult.QualityScore)

	// Step 3.5: Persist individual cognitive events to MongoDB
	eventsCollection := s.db.Collection(CognitiveEventsCollection)
	eventDocs := make([]interface{}, len(events))
	for i := range events {
		events[i].Status = "in_mtm"
		events[i].EventIndex = i
		if events[i].ID.IsZero() {
			events[i].ID = primitive.NewObjectID()
		}
		eventDocs[i] = events[i]
	}
	if _, err := eventsCollection.InsertMany(ctx, eventDocs); err != nil {
		log.Printf("WARN: Failed to persist %d cognitive events for chain %s: %v", len(events), chainID, err)
		// Non-fatal: chain can still be created without individual events
	} else {
		log.Printf("INFO: Persisted %d cognitive events for chain %s", len(events), chainID)
	}

	// Step 4: (Merge/Create)
	// This function will handle the actual persistence of the Chain and Events
	finalChain, err := s.sessionManager.ProcessNewChain(ctx, candidateChain, events)
	if err != nil {
		return fmt.Errorf("failed to process chain with session manager: %w", err)
	}
	log.Printf("INFO: Session manager processed chain %s, final ID: %s", candidateChain.ChainID, finalChain.ChainID)

	// Post-processing: Store embedding for the final chain
	if finalChain.Summary != "" {
		embedding, err := s.CreateEmbedding(ctx, finalChain.Summary)
		if err != nil {
			log.Printf("WARN: Failed to create embedding for chain %s: %v", finalChain.ChainID, err)
		} else {
			if err := s.StoreChainEmbedding(ctx, finalChain, embedding); err != nil {
				log.Printf("WARN: Failed to store embedding for chain %s: %v", finalChain.ChainID, err)
			} else {
				log.Printf("INFO: Stored embedding for chain %s", finalChain.ChainID)
			}
		}
	}

	// Post-processing: Index individual high-value events for semantic search
	if err := s.IndexCognitiveEvents(ctx, finalChain.ChainID, events); err != nil {
		// Log as a warning, as this is a non-critical background task
		log.Printf("WARN: Failed to index cognitive events for chain %s: %v", finalChain.ChainID, err)
	}

	// Post-processing: Cleanup old chains for the user
	if err := s.sessionManager.CleanupOldChains(ctx, userID); err != nil {
		log.Printf("WARN: Failed to cleanup old chains for user %s: %v", userID, err)
	}

	log.Printf("INFO: MTM formation complete for chain %s.", finalChain.ChainID)
	return nil
}

// StoreCognitiveEvent stores a single cognitive event in MongoDB
func (s *STMStore) StoreCognitiveEvent(ctx context.Context, event *models.CognitiveEvent) (primitive.ObjectID, error) {
	collection := s.db.Collection(CognitiveEventsCollection)
	if event.ID.IsZero() {
		event.ID = primitive.NewObjectID()
	}
	_, err := collection.InsertOne(ctx, event)
	if err != nil {
		return primitive.NilObjectID, fmt.Errorf("failed to insert cognitive event: %w", err)
	}
	return event.ID, nil
}

// analyzeTopicContinuity uses the LLM endpoint to analyze topic continuity
func (s *STMStore) analyzeTopicContinuity(ctx context.Context, userID string, previousContent string, newContent string) (bool, error) {
	llmBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")

	if llmBaseURL == "" {
		return false, fmt.Errorf("llm_base_url not configured")
	}
	if apiKey == "" {
		return false, fmt.Errorf("azure_openai_api_key not configured")
	}

	if !s.llmGuards.checkRateLimit(ctx, userID) {
		return false, fmt.Errorf("llm rate limit exceeded for user %s", userID)
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return false, fmt.Errorf("llm circuit breaker is open")
	}

	prompt := fmt.Sprintf(`You are a topic boundary detector. Determine if two conversation messages are about the SAME topic or DIFFERENT topics.

Rules:
- "Same topic" means they discuss the same subject, task, or line of inquiry
- "Different topic" means the user shifted to a new subject, even if loosely related
- A question about a DIFFERENT technical area (e.g., workflow design → authentication) is a topic change
- Asking "by the way" or "also" about something new is a topic change
- Follow-up questions that deepen the SAME subject are NOT a topic change

Previous message:
%s

New message:
%s

Is the new message about the SAME topic as the previous message? Respond with only "true" or "false".`,
		previousContent, newContent)

	request := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  10,
		"temperature": 0.1,
		"stop":        []string{"\n"},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return false, fmt.Errorf("failed to marshal llm request: %w", err)
	}

	llmTimeout := parseIntEnv("LLM_TIMEOUT_SECONDS", 10)
	httpCtx, cancel := context.WithTimeout(ctx, time.Duration(llmTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", llmBaseURL, bytes.NewBuffer(requestBody))
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("failed to make llm api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("llm api returned status %d", resp.StatusCode)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("failed to read response body: %w", err)
	}

	var llmResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("failed to unmarshal llm response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		s.llmGuards.recordLLMResult(false) // Record failure
		return false, fmt.Errorf("no choices in llm response")
	}

	s.llmGuards.recordLLMResult(true) // Record success

	responseText := strings.ToLower(strings.TrimSpace(llmResponse.Choices[0].Message.Content))
	return responseText == "true", nil
}

// CreateEmbedding performs vector embedding creation using an external service like Azure OpenAI
func (s *STMStore) CreateEmbedding(ctx context.Context, textToEmbed string) (*models.EmbeddingData, error) {
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	url := os.Getenv("EMBEDDING_BASE_URL")
	embeddingModel := os.Getenv("EMBEDDING_MODEL_NAME")

	if url == "" {
		return nil, fmt.Errorf("embedding_base_url environment variable not set")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("azure openai api key not configured")
	}

	if embeddingModel == "" {
		embeddingModel = "text-embedding-ada-002"
	}

	// Check embedding cache first
	if cachedEmbedding := s.getEmbeddingFromCache(ctx, textToEmbed, embeddingModel); cachedEmbedding != nil {
		log.Printf("INFO: Embedding cache hit for text (len=%d)", len(textToEmbed))
		return cachedEmbedding, nil
	}

	if !s.llmGuards.checkRateLimit(ctx, "embedding_user") {
		return nil, fmt.Errorf("embedding api rate limit exceeded")
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return nil, fmt.Errorf("embedding api circuit breaker is open")
	}

	embeddingRequest := map[string]interface{}{"input": textToEmbed}
	body, err := json.Marshal(embeddingRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, s.llmConfig.EmbeddingTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to call embedding service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.llmGuards.recordLLMResult(false) // Record failure
		responseBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure openai api returned status %d: %s", resp.StatusCode, string(responseBody))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to read embedding response: %w", err)
	}

	var embeddingResponse struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(responseBody, &embeddingResponse); err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	if len(embeddingResponse.Data) == 0 || len(embeddingResponse.Data[0].Embedding) == 0 {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("no embedding data in response")
	}

	s.llmGuards.recordLLMResult(true) // Record success

	embeddingData := &models.EmbeddingData{
		Vector:     embeddingResponse.Data[0].Embedding,
		Dimensions: len(embeddingResponse.Data[0].Embedding),
		Model:      embeddingModel,
		CreatedAt:  time.Now(),
	}

	// Store in cache for future reuse
	s.storeEmbeddingInCache(ctx, textToEmbed, embeddingModel, embeddingData)

	return embeddingData, nil
}

// getEmbeddingFromCache retrieves a cached embedding for the given text and model
func (s *STMStore) getEmbeddingFromCache(ctx context.Context, text, model string) *models.EmbeddingData {
	if s.redis == nil {
		return nil
	}

	cacheKey := s.generateEmbeddingCacheKey(text, model)
	var cachedData models.EmbeddingData

	err := s.redis.Get(cacheKey, &cachedData)
	if err != nil {
		// Cache miss or error - not critical
		return nil
	}

	return &cachedData
}

// storeEmbeddingInCache stores an embedding in the cache
func (s *STMStore) storeEmbeddingInCache(ctx context.Context, text, model string, embedding *models.EmbeddingData) {
	if s.redis == nil {
		return
	}

	cacheKey := s.generateEmbeddingCacheKey(text, model)
	// Cache embeddings for 24 hours
	cacheTTL := 24 * time.Hour

	err := s.redis.SetWithTTL(cacheKey, embedding, cacheTTL)
	if err != nil {
		log.Printf("WARN: Failed to cache embedding: %v", err)
	}
}

// generateEmbeddingCacheKey creates a consistent cache key from text and model
func (s *STMStore) generateEmbeddingCacheKey(text, model string) string {
	// Use SHA256 hash to create a fixed-length key from variable-length text
	hasher := sha256.New()
	hasher.Write([]byte(text))
	hasher.Write([]byte(model))
	hash := hex.EncodeToString(hasher.Sum(nil))
	return fmt.Sprintf("embedding:cache:%s", hash)
}

// acquireProcessingLock attempts to acquire a lock for processing a chain
// Returns true if lock was acquired, false if chain is already being processed
func (s *STMStore) acquireProcessingLock(ctx context.Context, chainID string) bool {
	if s.redis == nil {
		// If Redis is not available, allow processing (fail-open for availability)
		log.Println("WARN: Redis not available for idempotency check, proceeding with processing")
		return true
	}

	lockKey := fmt.Sprintf("mtm:processing:lock:%s", chainID)
	lockTTL := 5 * time.Minute // Lock expires after 5 minutes to prevent deadlocks

	// Try to set the lock with NX (only if not exists)
	err := s.redis.SetWithTTL(lockKey, "processing", lockTTL)
	if err != nil {
		// Lock already exists or error occurred
		log.Printf("WARN: Failed to acquire processing lock for chain %s: %v", chainID, err)

		// Check if the key already exists to distinguish between "already processing" and other errors
		exists, checkErr := s.redis.Exists(lockKey)
		if checkErr == nil && exists {
			return false // Chain is already being processed
		}
		// For other errors, fail-open for availability
		return true
	}

	return true
}

// releaseProcessingLock releases the processing lock for a chain
func (s *STMStore) releaseProcessingLock(ctx context.Context, chainID string) {
	if s.redis == nil {
		return
	}

	lockKey := fmt.Sprintf("mtm:processing:lock:%s", chainID)
	err := s.redis.Delete(lockKey)
	if err != nil {
		log.Printf("WARN: Failed to release processing lock for chain %s: %v", chainID, err)
	}
}

// CreateSegmentSummary generates a detailed summary and importance score for a segment using the LLM with Structured Outputs
func (s *STMStore) CreateSegmentSummary(ctx context.Context, events []models.CognitiveEvent) (string, float64, error) {
	LLMBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")

	if LLMBaseURL == "" {
		return "", 0, fmt.Errorf("llm_base_url not configured")
	}

	if apiKey == "" {
		return "", 0, fmt.Errorf("azure_openai_api_key not configured")
	}

	if !s.llmGuards.checkRateLimit(ctx, "summary_user") {
		return "", 0, fmt.Errorf("llm summary rate limit exceeded")
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return "", 0, fmt.Errorf("llm summary circuit breaker is open")
	}

	// Define strict JSON Schema for the response
	jsonSchema := map[string]interface{}{
		"name":   "mtm_summary_schema",
		"strict": true,
		"schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "A concise, detailed summary of the conversation segment.",
				},
				"intrinsic_importance": map[string]interface{}{
					"type":        "number",
					"description": "A float between 0.0 and 1.0 indicating the core value or permanence of this memory. 0.1 is trivial chitchat, 0.5 is standard context, 0.9+ is a critical system fact, core user preference, or hard constraint.",
				},
			},
			"required":             []string{"summary", "intrinsic_importance"},
			"additionalProperties": false,
		},
	}

	var b strings.Builder
	b.WriteString("You are a memory analysis agent. Analyze the following conversation segment.\n")
	b.WriteString("1. Generate a concise, detailed summary of the core topic, key facts, and reasoning.\n")
	b.WriteString("2. Evaluate the 'intrinsic_importance' of this segment based on its long-term value.\n")
	b.WriteString("\nEvent Chain:\n---")
	for _, event := range events {
		b.WriteString(fmt.Sprintf("%s: [%s] %s\n", event.Role, event.Type, event.Content))
	}
	b.WriteString("---\n\nRespond with the required JSON object.")

	prompt := b.String()

	reqBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant designed to output JSON."},
			{"role": "user", "content": prompt},
		},
		"max_tokens":  256,
		"temperature": 0.3,
		"response_format": map[string]interface{}{
			"type":        "json_schema",
			"json_schema": jsonSchema,
		},
	}
	body, _ := json.Marshal(reqBody)
	httpCtx, cancel := context.WithTimeout(ctx, s.llmConfig.SummaryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, "POST", LLMBaseURL, bytes.NewBuffer(body))
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.llmGuards.recordLLMResult(false) // Record failure
		return "", 0, fmt.Errorf("llm summary API returned %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &llmResp); err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return "", 0, err
	}
	if len(llmResp.Choices) == 0 {
		s.llmGuards.recordLLMResult(false) // Record failure
		return "", 0, fmt.Errorf("empty summary choices")
	}

	// Parse the structured output
	type MTMSummaryResponse struct {
		Summary             string  `json:"summary"`
		IntrinsicImportance float64 `json:"intrinsic_importance"`
	}
	var summaryResp MTMSummaryResponse
	if err := json.Unmarshal([]byte(llmResp.Choices[0].Message.Content), &summaryResp); err != nil {
		s.llmGuards.recordLLMResult(false)
		return "", 0, fmt.Errorf("failed to parse structured output: %w", err)
	}

	s.llmGuards.recordLLMResult(true) // Record success
	return summaryResp.Summary, summaryResp.IntrinsicImportance, nil
}

// StoreChainEmbedding stores an embedding for a cognitive chain summary in Milvus
func (s *STMStore) StoreChainEmbedding(ctx context.Context, chain *models.CognitiveChain, embedding *models.EmbeddingData) error {
	if s.milvus == nil {
		return fmt.Errorf("milvus client not configured")
	}
	return s.milvus.InsertSegmentEmbedding(ctx, chain.TenantID, chain.UserID, chain.AgentID, chain.ChainID, embedding)
}

// StoreEventEmbedding stores an embedding for a single cognitive event in Milvus.
// NOTE: This currently uses the same Milvus collection as chain summaries.
func (s *STMStore) StoreEventEmbedding(ctx context.Context, event *models.CognitiveEvent, embedding *models.EmbeddingData) error {
	if s.milvus == nil {
		return fmt.Errorf("milvus client not configured")
	}
	// Using event.ID.Hex() as the reference ID.
	return s.milvus.InsertSegmentEmbedding(ctx, event.TenantID, event.UserID, event.AgentID, event.ID.Hex(), embedding)
}

// IndexCognitiveEvents creates and stores embeddings for individual high-value cognitive events.
func (s *STMStore) IndexCognitiveEvents(ctx context.Context, chainID string, events []models.CognitiveEvent) error {
	if s.milvus == nil {
		log.Println("WARN: Milvus client not configured, skipping event indexing.")
		return nil // Not a fatal error
	}

	indexedCount := 0
	for _, event := range events {
		// Filter for high-value events
		shouldIndex := false
		switch event.Type {
		case models.STMEventTypeObservation, models.STMEventTypeThought:
			shouldIndex = true
		case models.STMEventTypeMessage:
			if len(event.Content) > 100 {
				shouldIndex = true
			}
		}

		if shouldIndex {
			// Create embedding for the event content
			embedding, err := s.CreateEmbedding(ctx, event.Content)
			if err != nil {
				log.Printf("WARN: Failed to create embedding for event %s in chain %s: %v", event.ID.Hex(), chainID, err)
				continue // Skip this event
			}

			// Store the embedding in Milvus using the event's ID as the reference
			err = s.StoreEventEmbedding(ctx, &event, embedding)
			if err != nil {
				log.Printf("WARN: Failed to store embedding for event %s in chain %s: %v", event.ID.Hex(), chainID, err)
				continue // Skip this event
			}
			indexedCount++
		}
	}

	if indexedCount > 0 {
		log.Printf("INFO: Indexed %d individual cognitive events for chain %s.", indexedCount, chainID)
	}

	return nil
}

// SearchSimilarChains finds cognitive chains that are semantically similar to a query vector.
func (s *STMStore) SearchSimilarChains(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]*models.CognitiveChain, error) {
	if s.milvus == nil {
		log.Println("WARN: Milvus client not configured, cannot search for similar chains.")
		return nil, nil // Return empty slice, not an error
	}

	// Step A: Call Milvus to get IDs of similar chains
	chainIDs, _, err := s.milvus.SearchSimilarSegments(ctx, tenantID, userID, agentID, queryVector, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search for similar segments in milvus: %w", err)
	}

	if len(chainIDs) == 0 {
		return []*models.CognitiveChain{}, nil // No similar chains found
	}

	log.Printf("INFO: Vector search found %d candidate chain IDs.", len(chainIDs))

	// Step B: Fetch the full metadata for those chain IDs from MongoDB
	collection := s.db.Collection(CognitiveChainsCollection)
	filter := bson.M{
		"chainId": bson.M{"$in": chainIDs},
		"status":  "active", // Only consider active chains
		"userId":  userID,   // Ensure chains belong to the same user
	}

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar chains from mongodb: %w", err)
	}
	defer cursor.Close(ctx)

	var chains []*models.CognitiveChain
	if err := cursor.All(ctx, &chains); err != nil {
		return nil, fmt.Errorf("failed to decode similar chains from mongodb: %w", err)
	}

	log.Printf("INFO: Fetched %d full cognitive chain documents from MongoDB.", len(chains))

	return chains, nil
}

// ArchiveColdChains scans for cold chains (low heat score) and archives them.
// Archiving involves deleting the vector embedding from Milvus and updating the status in MongoDB.
func (s *STMStore) ArchiveColdChains(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	// 1. Define criteria for "potentially cold" chains to scan.
	// We look for active chains that haven't been accessed in a while (e.g., 7 days).
	// This optimization avoids scanning the entire database every time.
	archiveScanDays := parseIntEnv("MTM_ARCHIVE_SCAN_DAYS", 7)
	scanThreshold := time.Now().AddDate(0, 0, -archiveScanDays)

	filter := bson.M{
		"status": "active",
		"$or": []bson.M{
			{"lastAccessedAt": bson.M{"$lt": scanThreshold}},
			{"lastAccessedAt": bson.M{"$exists": false}, "lastEventAt": bson.M{"$lt": scanThreshold}},
		},
	}

	collection := s.db.Collection(CognitiveChainsCollection)
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to query potential cold chains: %w", err)
	}
	defer cursor.Close(ctx)

	var chains []models.CognitiveChain
	if err := cursor.All(ctx, &chains); err != nil {
		return 0, fmt.Errorf("failed to decode potential cold chains: %w", err)
	}

	if len(chains) == 0 {
		return 0, nil
	}

	log.Printf("INFO: ArchiveColdChains found %d candidates for analysis.", len(chains))

	// 2. Initialize HeatScorer to calculate current heat
	heatScorer := NewHeatScorer(s.db)
	freezingPoint := parseFloatEnv("MTM_FREEZING_POINT", 0.1)

	archivedCount := 0

	for _, chain := range chains {
		// Calculate current heat score
		currentHeat, _, err := heatScorer.ComputeSegmentHeat(ctx, &chain)
		if err != nil {
			log.Printf("WARN: Failed to compute heat for chain %s: %v", chain.ChainID, err)
			continue
		}

		// Check if below freezing point
		if currentHeat < freezingPoint {
			log.Printf("INFO: Archiving cold chain %s (Heat: %.3f < %.3f)", chain.ChainID, currentHeat, freezingPoint)

			// A. Delete vector from Milvus
			if s.milvus != nil {
				if err := s.milvus.DeleteSegmentEmbedding(ctx, chain.TenantID, chain.UserID, chain.AgentID, chain.ChainID); err != nil {
					log.Printf("ERROR: Failed to delete vector for chain %s: %v", chain.ChainID, err)
					// We continue to mark as archived in Mongo, assuming Milvus might be down or data inconsistent.
					// Alternatively, we could skip archiving in Mongo to retry later.
					// For now, let's proceed to ensure Mongo state reflects "archived" status.
				}
			} else {
				log.Println("WARN: Milvus client not available, skipping vector deletion.")
			}

			// B. Update status in MongoDB
			now := time.Now()
			update := bson.M{
				"$set": bson.M{
					"status":     "archived",
					"archivedAt": now,
					"heatScore":  currentHeat, // Persist the final low score
				},
			}
			_, err := collection.UpdateOne(ctx, bson.M{"_id": chain.ID}, update)
			if err != nil {
				log.Printf("ERROR: Failed to update status for chain %s in MongoDB: %v", chain.ChainID, err)
			} else {
				archivedCount++
			}
		}
	}

	log.Printf("INFO: ArchiveColdChains completed. Archived %d/%d candidates.", archivedCount, len(chains))
	return archivedCount, nil
}
