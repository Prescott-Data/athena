package memory

import (
	"bytes"
	"context"
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
	"bitbucket.org/dromos/memory-os/internal/models"

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
}

const (
	// CognitiveEventsCollection is the MongoDB collection name for cognitive events
	CognitiveEventsCollection = "cognitive_events"
	// CognitiveChainsCollection is the MongoDB collection name for cognitive chains
	CognitiveChainsCollection = "cognitive_chains"
)

// parseFloatEnv reads a float from env with default
func parseFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// parseIntEnv reads an int from env with default
func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
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

// cosineSimilarity computes cosine similarity between two vectors with validation and normalization
func cosineSimilarity(a, b []float64) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, fmt.Errorf("empty vector(s): a=%d, b=%d dimensions", len(a), len(b))
	}

	// Validate dimensions match
	if len(a) != len(b) {
		return 0, fmt.Errorf("dimension mismatch: a=%d, b=%d", len(a), len(b))
	}

	// Normalize vectors to unit length
	normA := normalizeVector(a)
	normB := normalizeVector(b)

	// Check for zero vectors after normalization
	var sumA, sumB float64
	for i := range normA {
		sumA += normA[i] * normA[i]
		sumB += normB[i] * normB[i]
	}
	if sumA == 0 || sumB == 0 {
		return 0, fmt.Errorf("zero vector after normalization")
	}

	// Compute dot product (cosine similarity for unit vectors)
	var dot float64
	for i := range normA {
		dot += normA[i] * normB[i]
	}

	// Clamp to [-1, 1] to handle floating point precision issues
	if dot > 1.0 {
		dot = 1.0
	} else if dot < -1.0 {
		dot = -1.0
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

// STMStore manages the Short-Term Memory store operations
type STMStore struct {
	db         *mongo.Database
	redis      cache.Interface
	milvus     *MilvusClient
	llmGuards  *LLMGuardrails
	HTTPClient *http.Client
}

// NewSTMStore creates a new STM store instance
func NewSTMStore(database *mongo.Database, redisClient cache.Interface) *STMStore {
	MilvusHost := os.Getenv("MILVUS_HOST")
	MilvusPort := os.Getenv("MILVUS_PORT")

	// Initialize Milvus client
	var milvusClient *MilvusClient
	if MilvusHost != "" && MilvusPort != "" {
		var err error
		milvusClient, err = NewMilvusClient(MilvusHost, MilvusPort)
		if err != nil {
			log.Printf("WARN: Failed to initialize Milvus client: %v", err)
			log.Println("WARN: Vector similarity search will be disabled")
		} else {
			log.Println("INFO: Milvus client initialized successfully")
		}
	} else {
		log.Println("WARN: Milvus configuration not provided, vector similarity search will be disabled")
	}

	return &STMStore{
		db:     database,
		redis:  redisClient,
		milvus: milvusClient,
		llmGuards: &LLMGuardrails{
			redis: redisClient,
		},
		HTTPClient: &http.Client{},
	}
}

// checkRateLimit checks if user has exceeded LLM call rate limit
func (lg *LLMGuardrails) checkRateLimit(ctx context.Context, userID string) bool {
	_ = ctx
	if lg.redis == nil {
		return true // Allow if Redis unavailable
	}
	LLMRateLimit := parseIntEnv("LLM_RATE_LIMIT_PER_MINUTE", 50)
	rateLimitKey := fmt.Sprintf("llm_rate_limit:%s", userID)

	// Get current count
	var count int
	err := lg.redis.Get(rateLimitKey, &count)
	if err != nil {
		// Key doesn't exist or error, start fresh
		count = 0
	}

	if count >= LLMRateLimit {
		log.Printf("WARN: LLM rate limit exceeded for user %s: %d/%d calls", userID, count, LLMRateLimit)
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

	LLMCircuitBreakerTimeout := parseIntEnv("LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS", 60)

	// Check if timeout has passed
	if time.Since(lg.circuitBreakerOpened) > time.Duration(LLMCircuitBreakerTimeout)*time.Second {
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
		LLMCircuitBreakerThreshold := parseIntEnv("LLM_CIRCUIT_BREAKER_THRESHOLD", 5)
		lg.failureCount++
		if lg.failureCount >= LLMCircuitBreakerThreshold && !lg.circuitBreakerOpen {
			lg.circuitBreakerOpen = true
			lg.circuitBreakerOpened = time.Now()
			log.Printf("WARN: LLM circuit breaker opened after %d failures", lg.failureCount)
		}
	}
}

// analyzeTopicContinuity uses the LLM endpoint to analyze topic continuity
func (s *STMStore) analyzeTopicContinuity(ctx context.Context, userID string, previousContent string, newContent string) (bool, error) {

	llmBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")

	if llmBaseURL == "" {
		return false, fmt.Errorf("LLM_BASE_URL not configured")
	}

	if apiKey == "" {
		return false, fmt.Errorf("AZURE_OPENAI_API_KEY not configured")
	}

	if !s.llmGuards.checkRateLimit(ctx, userID) {
		return false, fmt.Errorf("LLM rate limit exceeded for user %s", userID)
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return false, fmt.Errorf("LLM circuit breaker is open")
	}

	// Create prompt for the LLM to analyze topic continuity
	prompt := fmt.Sprintf(`Analyze whether the following two conversation turns are about the same topic or a continuation of the same conversation.

Previous turn:
%s

New turn:
%s

Respond with only "true" if the conversations are continuous/related or "false" if they represent a topic change or new conversation.`,
		previousContent, newContent)

	// Create LLM completion request
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
		return false, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	// Use more aggressive timeout for LLM fallback calls
	llmTimeout := parseIntEnv("LLM_TIMEOUT_SECONDS", 10) // This is fine, as it's a "def"
	httpCtx, cancel := context.WithTimeout(ctx, time.Duration(llmTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", llmBaseURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return false, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey) // Use the local apiKey variable

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to make LLM API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("LLM API returned status %d", resp.StatusCode)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the LLM response
	var llmResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return false, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		return false, fmt.Errorf("no choices in LLM response")
	}

	// Parse the response text to determine continuity
	responseText := strings.ToLower(strings.TrimSpace(llmResponse.Choices[0].Message.Content))
	return responseText == "true", nil
}

// CreateEmbedding performs Step B: Vector Embedding Creation using Azure OpenAI
func (s *STMStore) CreateEmbedding(ctx context.Context, textToEmbed string) (*models.EmbeddingData, error) {
	start := time.Now()

	// --- FIX: Read variables inside the function ---
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	url := os.Getenv("EMBEDDING_BASE_URL")
	embeddingModel := os.Getenv("EMBEDDING_MODEL_NAME")
	// --- END FIX ---

	// Check Azure OpenAI configuration
	if url == "" {
		return nil, fmt.Errorf("EMBEDDING_BASE_URL environment variable not set")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("azure OpenAI API key not configured")
	}

	if !s.llmGuards.checkRateLimit(ctx, "embedding_user") {
		return nil, fmt.Errorf("embedding API rate limit exceeded")
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return nil, fmt.Errorf("embedding API circuit breaker is open")
	}

	// Use configured model or default to text-embedding-ada-002
	if embeddingModel == "" {
		embeddingModel = "text-embedding-ada-002"
	}

	// Create Azure OpenAI embedding request
	embeddingRequest := map[string]interface{}{
		"input": textToEmbed,
	}

	requestBody, err := json.Marshal(embeddingRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Azure OpenAI embedding request: %w", err)
	}

	// Make API call to Azure OpenAI
	httpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to create Azure OpenAI embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to make Azure OpenAI embedding API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.llmGuards.recordLLMResult(false) // Record failure
		responseBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure OpenAI API returned status %d: %s", resp.StatusCode, string(responseBody))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("failed to read Azure OpenAI embedding response: %w", err)
	}

	// Parse Azure OpenAI embedding response
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
		return nil, fmt.Errorf("failed to unmarshal Azure OpenAI embedding response: %w", err)
	}

	if len(embeddingResponse.Data) == 0 || len(embeddingResponse.Data[0].Embedding) == 0 {
		s.llmGuards.recordLLMResult(false) // Record failure
		return nil, fmt.Errorf("no embedding data in Azure OpenAI response")
	}

	s.llmGuards.recordLLMResult(true) // Record success

	embedding := &models.EmbeddingData{
		Vector:     embeddingResponse.Data[0].Embedding,
		Dimensions: len(embeddingResponse.Data[0].Embedding),
		Model:      embeddingModel,
		CreatedAt:  time.Now(),
	}

	duration := time.Since(start)
	// MetricEmbeddingLatency.Observe(duration.Seconds())
	log.Printf("INFO: Azure OpenAI embedding created - Model: %s, Dimensions: %d, Duration: %v, Tokens: %d",
		embedding.Model, embedding.Dimensions, duration, embeddingResponse.Usage.TotalTokens)

	return embedding, nil
}

// StoreCognitiveEvent stores a single cognitive event in MongoDB
func (s *STMStore) StoreCognitiveEvent(ctx context.Context, event *models.CognitiveEvent) (primitive.ObjectID, error) {
	collection := s.db.Collection(CognitiveEventsCollection)
	result, err := collection.InsertOne(ctx, event)
	if err != nil {
		return primitive.ObjectID{}, fmt.Errorf("failed to insert cognitive event: %w", err)
	}
	return result.InsertedID.(primitive.ObjectID), nil
}

// CreateSegmentSummary generates a one-sentence summary for a segment using the LLM
func (s *STMStore) CreateSegmentSummary(ctx context.Context, events []models.CognitiveEvent) (string, error) {
	LLMBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")

	if LLMBaseURL == "" {
		return "", fmt.Errorf("LLM_BASE_URL not configured")
	}

	if apiKey == "" {
		return "", fmt.Errorf("AZURE_OPENAI_API_KEY not configured")
	}

	if !s.llmGuards.checkRateLimit(ctx, "summary_user") {
		return "", fmt.Errorf("LLM summary rate limit exceeded")
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return "", fmt.Errorf("LLM summary circuit breaker is open")
	}

	var b strings.Builder
	b.WriteString("You are a memory analysis agent. Your task is to create a concise, one-sentence summary of the following cognitive event chain. The summary should capture the core topic, key facts, and the agent's reasoning process.\n\nHere is the event chain:\n---")
	for _, event := range events {
		b.WriteString(fmt.Sprintf("%s: [%s] %s\n", event.Role, event.Type, event.Content))
	}
	b.WriteString("---\n\nFocus on the \"why\" behind the agent's actions, as revealed in its thoughts. The summary should be from the perspective of the agent.\n\nOne-sentence summary:")

	prompt := b.String()

	reqBody := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  64,
		"temperature": 0.3,
		"stop":        []string{"\n"},
	}
	body, _ := json.Marshal(reqBody)
	httpCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, "POST", LLMBaseURL, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	req.Header.Set("api-key", apiKey)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM summary API returned %d", resp.StatusCode)
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
		return "", err
	}
	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("empty summary choices")
	}
	return strings.TrimSpace(llmResp.Choices[0].Message.Content), nil
}

// ProcessMTMFormation orchestrates the MTM persistence pipeline
func (s *STMStore) ProcessMTMFormation(ctx context.Context, tenantID, userID, agentID string, events []models.CognitiveEvent) error {
	// 1. Create a summary of the events
	summary, err := s.CreateSegmentSummary(ctx, events)
	if err != nil {
		return fmt.Errorf("failed to create segment summary: %w", err)
	}

	// 2. Create an embedding of the summary
	embedding, err := s.CreateEmbedding(ctx, summary)
	if err != nil {
		return fmt.Errorf("failed to create summary embedding: %w", err)
	}

	// 3. Create and save the CognitiveChain metadata
	chainID := fmt.Sprintf("chain_%s_%d", userID, time.Now().Unix())
	chain := &models.CognitiveChain{
		TenantID:    tenantID,
		UserID:      userID,
		AgentID:     agentID,
		ChainID:     chainID,
		Topic:       "<placeholder>", // Placeholder, could be extracted by another LLM call
		Summary:     summary,
		StartedAt:   events[0].CreatedAt,
		LastEventAt: events[len(events)-1].CreatedAt,
		EventCount:  len(events),
		Status:      "archived",
	}
	collection := s.db.Collection(CognitiveChainsCollection)
	_, err = collection.InsertOne(ctx, chain)
	if err != nil {
		return fmt.Errorf("failed to store cognitive chain metadata: %w", err)
	}

	// 4. Store the summary vector in Milvus
	if s.milvus != nil {
		embedding.ReferenceID = chainID
		if err := s.milvus.InsertSegmentEmbedding(ctx, tenantID, userID, agentID, chainID, embedding); err != nil {
			// Log the error but don't fail the whole process, as this is a secondary index
			log.Printf("WARN: Failed to store segment embedding in Milvus: %v", err)
		}
	}

	// 5. Save all the individual events to MongoDB
	for _, event := range events {
		event.ChainID = chainID
		if _, err := s.StoreCognitiveEvent(ctx, &event); err != nil {
			// Log the error but continue, to save as much data as possible
			log.Printf("WARN: Failed to store cognitive event %s: %v", event.ID, err)
		}
	}

	return nil
}

// StoreSegmentEmbedding creates an embedding for a segment summary and stores it with tenant scope
func (s *STMStore) StoreSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string, embedding *models.EmbeddingData) error {
	if s.milvus == nil {
		return fmt.Errorf("milvus client not configured for segment embeddings")
	}
	return s.milvus.InsertSegmentEmbedding(ctx, tenantID, userID, agentID, segmentID, embedding)
}
