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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

const (
	// DialoguePagesCollection is the MongoDB collection name for dialogue pages
	DialoguePagesCollection = "dialogue_pages"
	// DialogueChainsCollection is the MongoDB collection name for dialogue chains
	DialogueChainsCollection = "dialogue_chains"
)

var (
	// AIAgentBaseURL is the base URL for the AI Agent service
	AIAgentBaseURL = os.Getenv("AI_AGENT_BASE_URL")
	// LLMBaseURL is the base URL for the LLM service
	LLMBaseURL = os.Getenv("LLM_BASE_URL")
	// LLMModelName is the model name for the LLM service
	LLMModelName = os.Getenv("LLM_MODEL_NAME")
	// Azure OpenAI configuration
	AzureOpenAIEndpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")        // https://dromos-open-ai.openai.azure.com
	AzureOpenAIAPIKey   = os.Getenv("AZURE_OPENAI_API_KEY")         // Azure OpenAI API key
	EmbeddingBaseURL    = os.Getenv("EMBEDDING_BASE_URL")           // Full embedding endpoint URL
	EmbeddingModelName  = os.Getenv("EMBEDDING_MODEL_NAME")         // Default: text-embedding-ada-002
	EmbeddingAPIVersion = os.Getenv("EMBEDDING_API_VERSION")        // Default: 2023-05-15
	EmbeddingDimensions = parseIntEnv("EMBEDDING_DIMENSIONS", 1536) // Azure OpenAI text-embedding-ada-002
	// MilvusHost is the Milvus server host
	MilvusHost = os.Getenv("MILVUS_HOST")
	// MilvusPort is the Milvus server port
	MilvusPort = os.Getenv("MILVUS_PORT")
	// MilvusDatabase is the Milvus database name
	MilvusDatabase = os.Getenv("MILVUS_DATABASE")

	// Cosine gate thresholds (env-tunable)
	ChainSimHigh = parseFloatEnv("CHAIN_SIM_HIGH", 0.72)
	ChainSimLow  = parseFloatEnv("CHAIN_SIM_LOW", 0.52)

	// LLM guardrails (env-tunable)
	LLMRateLimit               = parseIntEnv("LLM_RATE_LIMIT_PER_MINUTE", 50)           // calls per minute per user
	LLMCircuitBreakerThreshold = parseIntEnv("LLM_CIRCUIT_BREAKER_THRESHOLD", 5)        // failures before opening
	LLMCircuitBreakerTimeout   = parseIntEnv("LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS", 60) // seconds to wait before retry
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
	db        *mongo.Database
	redis     cache.Interface
	milvus    *MilvusClient
	llmGuards *LLMGuardrails
}

// NewSTMStore creates a new STM store instance
func NewSTMStore(database *mongo.Database, redisClient cache.Interface) *STMStore {
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
	}
}

// checkRateLimit checks if user has exceeded LLM call rate limit
func (lg *LLMGuardrails) checkRateLimit(ctx context.Context, userID string) bool {
	if lg.redis == nil {
		return true // Allow if Redis unavailable
	}

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
		lg.failureCount++
		if lg.failureCount >= LLMCircuitBreakerThreshold && !lg.circuitBreakerOpen {
			lg.circuitBreakerOpen = true
			lg.circuitBreakerOpened = time.Now()
			log.Printf("WARN: LLM circuit breaker opened after %d failures", lg.failureCount)
		}
	}
}

// DetermineDialogueChain performs Step A: Dialogue Chain Analysis
func (s *STMStore) DetermineDialogueChain(ctx context.Context, tenantID, userID, agentID, userMessage, agentResponse string) (string, error) {
	start := time.Now()
	defer func() {
		MetricDialogueChainLatency.Observe(time.Since(start).Seconds())
	}()

	// Get the most recent dialogue page for this user within tenant/agent scope
	previousPage, err := s.getLastDialoguePage(ctx, tenantID, userID, agentID)
	if err != nil && err != mongo.ErrNoDocuments {
		return "", fmt.Errorf("failed to retrieve previous dialogue page: %w", err)
	}

	var chainID string

	if previousPage == nil {
		// First conversation turn - create new chain
		chainID = fmt.Sprintf("chain_%s_%d", userID, time.Now().Unix())

		// Create dialogue chain metadata
		chain := &models.DialogueChain{
			TenantID:   tenantID,
			UserID:     userID,
			AgentID:    agentID,
			ChainID:    chainID,
			Topic:      "New conversation", // Will be updated later
			Summary:    fmt.Sprintf("Started with: %s", userMessage[:min(100, len(userMessage))]),
			StartedAt:  time.Now(),
			LastTurnAt: time.Now(),
			TurnCount:  1,
			Status:     "active",
		}

		if err := s.createDialogueChain(ctx, chain); err != nil {
			log.Printf("WARN: Failed to create dialogue chain metadata: %v", err)
		}

		duration := time.Since(start)
		log.Printf("INFO: New dialogue chain created - UserID: %s, ChainID: %s, Duration: %v",
			userID, chainID, duration)

		return chainID, nil
	}

	// Cosine-gate: fast check before LLM
	var isContinuous bool
	var needsLLM bool
	{
		// Embed current turn
		curEmb, err := s.CreateEmbedding(ctx, userMessage, agentResponse)
		if err != nil {
			log.Printf("WARN: Cosine-gate: failed to embed current turn, falling back to LLM: %v", err)
			needsLLM = true
		} else {
			// Try to retrieve stored embedding for previous turn, fallback to recomputing
			var prevEmb *models.EmbeddingData
			if previousPage.ID != primitive.NilObjectID {
				prevEmb, err = s.GetEmbedding(ctx, tenantID, userID, agentID, previousPage.ID.Hex())
				if err != nil {
					log.Printf("INFO: Stored embedding not found for previous turn, recomputing: %v", err)
				}
			}

			// Fallback to recomputing if no stored embedding found
			if prevEmb == nil {
				prevEmb, err = s.CreateEmbedding(ctx, previousPage.UserMessage, previousPage.AgentResponse)
				if err != nil {
					log.Printf("WARN: Cosine-gate: failed to embed previous turn, falling back to LLM: %v", err)
					needsLLM = true
				}
			}

			if prevEmb != nil {
				sim, err := cosineSimilarity(curEmb.Vector, prevEmb.Vector)
				if err != nil {
					log.Printf("WARN: Cosine similarity calculation failed: %v, falling back to LLM", err)
					MetricLLMFallbackCalls.WithLabelValues("embedding_failure", "").Inc()
					needsLLM = true
				} else {
					// Record similarity distribution
					MetricCosineSimilarity.Observe(sim)
					log.Printf("INFO: Cosine-gate similarity=%.3f (high=%.2f low=%.2f)", sim, ChainSimHigh, ChainSimLow)

					if sim >= ChainSimHigh {
						isContinuous = true
						needsLLM = false
						MetricCosineGateDecisions.WithLabelValues("high", "continue").Inc()
					} else if sim <= ChainSimLow {
						isContinuous = false
						needsLLM = false
						MetricCosineGateDecisions.WithLabelValues("low", "new_chain").Inc()
					} else {
						// Gray zone → will use LLM below
						needsLLM = true
						MetricCosineGateDecisions.WithLabelValues("gray_zone", "llm_fallback").Inc()
					}
				}
			}
		}
	}

	// Only call LLM for gray zone or embedding failures (with guardrails)
	if needsLLM {
		// Check rate limit and circuit breaker
		if !s.llmGuards.checkRateLimit(ctx, userID) {
			log.Printf("WARN: LLM rate limit exceeded for user %s, defaulting to new chain", userID)
			MetricLLMFallbackCalls.WithLabelValues("rate_limited", "blocked").Inc()
			isContinuous = false
		} else if !s.llmGuards.checkCircuitBreaker() {
			log.Printf("WARN: LLM circuit breaker open, defaulting to new chain")
			MetricLLMFallbackCalls.WithLabelValues("circuit_breaker", "blocked").Inc()
			isContinuous = false
		} else {
			isContinuousLLM, err := s.analyzeTopicContinuity(ctx, previousPage, userMessage, agentResponse)
			if err != nil {
				log.Printf("WARN: Topic continuity analysis failed: %v, defaulting to new chain", err)
				MetricLLMFallbackCalls.WithLabelValues("gray_zone", "error").Inc()
				s.llmGuards.recordLLMResult(false)
				isContinuousLLM = false // Default to creating new chain on error
			} else {
				MetricLLMFallbackCalls.WithLabelValues("gray_zone", "success").Inc()
				s.llmGuards.recordLLMResult(true)
			}
			isContinuous = isContinuousLLM
		}
	}

	if isContinuous {
		// Continue existing chain
		chainID = previousPage.ChainID

		// Update chain metadata
		if err := s.updateDialogueChain(ctx, chainID); err != nil {
			log.Printf("WARN: Failed to update dialogue chain metadata: %v", err)
		}
	} else {
		// Create new chain
		chainID = fmt.Sprintf("chain_%s_%d", userID, time.Now().Unix())

		// Create dialogue chain metadata
		chain := &models.DialogueChain{
			ChainID:    chainID,
			UserID:     userID,
			Topic:      "Topic shift",
			Summary:    fmt.Sprintf("New topic: %s", userMessage[:min(100, len(userMessage))]),
			StartedAt:  time.Now(),
			LastTurnAt: time.Now(),
			TurnCount:  1,
			Status:     "active",
		}

		if err := s.createDialogueChain(ctx, chain); err != nil {
			log.Printf("WARN: Failed to create new dialogue chain metadata: %v", err)
		}
	}

	duration := time.Since(start)
	log.Printf("INFO: Dialogue chain analysis completed - UserID: %s, ChainID: %s, Continuous: %t, Duration: %v",
		userID, chainID, isContinuous, duration)

	return chainID, nil
}

// analyzeTopicContinuity uses the LLM endpoint to analyze topic continuity
func (s *STMStore) analyzeTopicContinuity(ctx context.Context, previousPage *models.DialoguePage, userMessage, agentResponse string) (bool, error) {
	if LLMBaseURL == "" {
		return false, fmt.Errorf("LLM_BASE_URL not configured")
	}

	// Create prompt for the LLM to analyze topic continuity
	prompt := fmt.Sprintf(`Analyze whether the following two conversation turns are about the same topic or a continuation of the same conversation.

Previous turn:
User: %s
Assistant: %s

New turn:
User: %s
Assistant: %s

Respond with only "true" if the conversations are continuous/related or "false" if they represent a topic change or new conversation.`,
		previousPage.UserMessage, previousPage.AgentResponse, userMessage, agentResponse)

	// Create LLM completion request
	modelName := LLMModelName
	if modelName == "" {
		modelName = "Qwen/Qwen3-32B-AWQ"
	}

	request := map[string]interface{}{
		"model":       modelName,
		"prompt":      prompt,
		"max_tokens":  10,
		"temperature": 0.1,
		"stop":        []string{"\n"},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return false, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	// Use more aggressive timeout for LLM fallback calls
	llmTimeout := parseIntEnv("LLM_TIMEOUT_SECONDS", 10)
	httpCtx, cancel := context.WithTimeout(ctx, time.Duration(llmTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", LLMBaseURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return false, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
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
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return false, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		return false, fmt.Errorf("no choices in LLM response")
	}

	// Parse the response text to determine continuity
	responseText := strings.ToLower(strings.TrimSpace(llmResponse.Choices[0].Text))
	return responseText == "true", nil
}

// CreateEmbedding performs Step B: Vector Embedding Creation using Azure OpenAI
func (s *STMStore) CreateEmbedding(ctx context.Context, userMessage, agentResponse string) (*models.EmbeddingData, error) {
	start := time.Now()

	// Check Azure OpenAI configuration
	if AzureOpenAIEndpoint == "" {
		return nil, fmt.Errorf("Azure OpenAI endpoint not configured")
	}
	if AzureOpenAIAPIKey == "" {
		return nil, fmt.Errorf("Azure OpenAI API key not configured")
	}

	// Use configured model or default to text-embedding-ada-002
	embeddingModel := EmbeddingModelName
	if embeddingModel == "" {
		embeddingModel = "text-embedding-ada-002"
	}

	// Combine user message and agent response for embedding
	combinedText := fmt.Sprintf("User: %s\nAgent: %s", userMessage, agentResponse)

	// Create Azure OpenAI embedding request
	embeddingRequest := map[string]interface{}{
		"input": combinedText,
		"model": embeddingModel,
	}

	requestBody, err := json.Marshal(embeddingRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Azure OpenAI embedding request: %w", err)
	}

	// Make API call to Azure OpenAI
	httpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=2023-05-15", AzureOpenAIEndpoint, embeddingModel)

	req, err := http.NewRequestWithContext(httpCtx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure OpenAI embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", AzureOpenAIAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make Azure OpenAI embedding API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure OpenAI API returned status %d: %s", resp.StatusCode, string(responseBody))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Azure OpenAI embedding response: %w", err)
	}

	// Parse Azure OpenAI embedding response
	var embeddingResponse struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			TotalTokens      int `json:"total_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(responseBody, &embeddingResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Azure OpenAI embedding response: %w", err)
	}

	if len(embeddingResponse.Data) == 0 || len(embeddingResponse.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding data in Azure OpenAI response")
	}

	embedding := &models.EmbeddingData{
		Vector:     embeddingResponse.Data[0].Embedding,
		Dimensions: len(embeddingResponse.Data[0].Embedding),
		Model:      embeddingModel,
		CreatedAt:  time.Now(),
	}

	duration := time.Since(start)
	MetricEmbeddingLatency.Observe(duration.Seconds())
	log.Printf("INFO: Azure OpenAI embedding created - Model: %s, Dimensions: %d, Duration: %v, Tokens: %d",
		embedding.Model, embedding.Dimensions, duration, embeddingResponse.Usage.TotalTokens)

	return embedding, nil
}

// validateAzureOpenAIConfig validates that Azure OpenAI is properly configured
func (s *STMStore) validateAzureOpenAIConfig() error {
	if AzureOpenAIEndpoint == "" {
		return fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable not set")
	}
	if AzureOpenAIAPIKey == "" {
		return fmt.Errorf("AZURE_OPENAI_API_KEY environment variable not set")
	}
	return nil
}

// StoreDialoguePage performs Step C: Write to STM Store (MongoDB)
func (s *STMStore) StoreDialoguePage(ctx context.Context, page *models.DialoguePage) (primitive.ObjectID, error) {
	return s.StoreDialoguePageWithContext(ctx, page, "", "", "")
}

// StoreDialoguePageWithContext stores a dialogue page using tenant/user/agent from context (enforced)
func (s *STMStore) StoreDialoguePageWithContext(ctx context.Context, page *models.DialoguePage, tenantID, userID, agentID string) (primitive.ObjectID, error) {
	start := time.Now()

	// Override tenant/user/agent values from authenticated context
	if tenantID != "" {
		page.TenantID = tenantID
	}
	if userID != "" {
		page.UserID = userID
	}
	if agentID != "" {
		page.AgentID = agentID
	}

	// Ensure timestamps are set
	if page.CreatedAt.IsZero() {
		page.CreatedAt = time.Now()
	}
	if page.UpdatedAt.IsZero() {
		page.UpdatedAt = time.Now()
	}

	// Insert into MongoDB
	collection := s.db.Collection(DialoguePagesCollection)
	result, err := collection.InsertOne(ctx, page)
	if err != nil {
		return primitive.ObjectID{}, fmt.Errorf("failed to insert dialogue page: %w", err)
	}

	pageID := result.InsertedID.(primitive.ObjectID)

	duration := time.Since(start)
	log.Printf("INFO: Dialogue page stored - PageID: %s, ChainID: %s, Duration: %v",
		pageID.Hex(), page.ChainID, duration)

	return pageID, nil
}

// GetEmbedding retrieves a stored embedding by page ID with tenant scope
func (s *STMStore) GetEmbedding(ctx context.Context, tenantID, userID, agentID, pageID string) (*models.EmbeddingData, error) {
	// Try Milvus first if available
	if s.milvus != nil {
		embedding, err := s.milvus.GetEmbeddingByPageID(ctx, tenantID, userID, agentID, pageID)
		if err == nil {
			return embedding, nil
		}
		log.Printf("WARN: Failed to retrieve embedding from Milvus for page %s: %v", pageID, err)
	}

	// Fall back to Redis
	return s.getEmbeddingFromRedis(ctx, pageID)
}

// generateEmbeddingKey creates the Redis key for embedding storage
func (s *STMStore) generateEmbeddingKey(pageID string) string {
	return fmt.Sprintf("embedding:%s", pageID)
}

// generateScopedEmbeddingKey creates the Redis key for embedding storage with tenant scope
func (s *STMStore) generateScopedEmbeddingKey(tenantID, userID, agentID, pageID string) string {
	return fmt.Sprintf("embedding:v1:%s:%s:%s:%s", tenantID, userID, agentID, pageID)
}

// getEmbeddingFromRedis retrieves embedding from Redis fallback storage
func (s *STMStore) getEmbeddingFromRedis(ctx context.Context, pageID string) (*models.EmbeddingData, error) {
	embeddingKey := s.generateEmbeddingKey(pageID)
	var embeddingJSON string
	err := s.redis.Get(embeddingKey, &embeddingJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding from Redis: %w", err)
	}

	var embedding models.EmbeddingData
	if err := json.Unmarshal([]byte(embeddingJSON), &embedding); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embedding: %w", err)
	}

	return &embedding, nil
}

// StoreEmbedding stores the vector embedding in Milvus with tenant scope
func (s *STMStore) StoreEmbedding(ctx context.Context, tenantID, userID, agentID, pageID string, embedding *models.EmbeddingData) error {
	start := time.Now()

	// Set the page reference
	embedding.PageID = pageID

	// Store in Milvus if available
	if s.milvus != nil {
		if err := s.milvus.InsertEmbedding(ctx, tenantID, userID, agentID, pageID, embedding); err != nil {
			log.Printf("WARN: Failed to store embedding in Milvus: %v", err)
			// Fall back to Redis storage
			return s.storeEmbeddingInRedis(ctx, pageID, embedding)
		}

		duration := time.Since(start)
		log.Printf("INFO: Embedding stored in Milvus - PageID: %s, Dimensions: %d, Duration: %v",
			pageID, embedding.Dimensions, duration)
		return nil
	}

	// Fall back to Redis storage
	return s.storeEmbeddingInRedis(ctx, pageID, embedding)
}

// storeEmbeddingInRedis stores embedding in Redis as fallback
func (s *STMStore) storeEmbeddingInRedis(ctx context.Context, pageID string, embedding *models.EmbeddingData) error {
	start := time.Now()

	embeddingKey := s.generateEmbeddingKey(pageID)
	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding: %w", err)
	}

	// Store with long TTL (30 days)
	if err := s.redis.SetEX(embeddingKey, string(embeddingJSON), 30*24*time.Hour); err != nil {
		return fmt.Errorf("failed to store embedding in Redis: %w", err)
	}

	duration := time.Since(start)
	log.Printf("INFO: Embedding stored in Redis (fallback) - PageID: %s, Dimensions: %d, Duration: %v",
		pageID, embedding.Dimensions, duration)

	return nil
}

// Helper functions

func (s *STMStore) getLastDialoguePage(ctx context.Context, tenantID, userID, agentID string) (*models.DialoguePage, error) {
	collection := s.db.Collection(DialoguePagesCollection)

	filter := bson.M{
		"tenantId": tenantID,
		"userId":   userID,
		"agentId":  agentID,
	}
	opts := options.FindOne()
	opts.SetSort(bson.D{bson.E{Key: "createdAt", Value: -1}}) // Most recent first

	var page models.DialoguePage
	err := collection.FindOne(ctx, filter, opts).Decode(&page)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // No previous pages found
		}
		return nil, err
	}

	return &page, nil
}

func (s *STMStore) createDialogueChain(ctx context.Context, chain *models.DialogueChain) error {
	collection := s.db.Collection(DialogueChainsCollection)
	_, err := collection.InsertOne(ctx, chain)
	return err
}

func (s *STMStore) updateDialogueChain(ctx context.Context, chainID string) error {
	collection := s.db.Collection(DialogueChainsCollection)

	filter := bson.M{"chainId": chainID}
	update := bson.M{
		"$set": bson.M{
			"lastTurnAt": time.Now(),
		},
		"$inc": bson.M{
			"turnCount": 1,
		},
	}

	_, err := collection.UpdateOne(ctx, filter, update)
	return err
}

// STM Store Retrieval Methods

// RetrieveFromSTMStore performs semantic search on stored memories with tenant scope
func (s *STMStore) RetrieveFromSTMStore(ctx context.Context, tenantID, userID, agentID, query string, limit int) ([]*models.DialoguePage, error) {
	start := time.Now()

	// Step 1: Create query embedding
	queryEmbedding, err := s.CreateEmbedding(ctx, query, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create query embedding: %w", err)
	}

	// Step 2: Find similar vectors from Redis (placeholder for vector DB)
	pageIDs, err := s.findSimilarEmbeddings(ctx, tenantID, userID, agentID, queryEmbedding.Vector, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar embeddings: %w", err)
	}

	// Step 3: Retrieve full dialogue pages from MongoDB
	pages, err := s.getDialoguePagesByIDs(ctx, pageIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve dialogue pages: %w", err)
	}

	duration := time.Since(start)
	log.Printf("INFO: STM Store retrieval completed - UserID: %s, Query: %s, Results: %d, Duration: %v",
		userID, query, len(pages), duration)

	return pages, nil
}

// RetrieveSegments performs segment-level search using segment embeddings with tenant scope
func (s *STMStore) RetrieveSegments(ctx context.Context, tenantID, userID, agentID, query string, limit int) ([]models.Segment, error) {
	if s.milvus == nil {
		return []models.Segment{}, nil
	}

	emb, err := s.CreateEmbedding(ctx, query, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create query embedding: %w", err)
	}

	segIDs, _, err := s.milvus.SearchSimilarSegments(ctx, tenantID, userID, agentID, emb.Vector, limit)
	if err != nil || len(segIDs) == 0 {
		return []models.Segment{}, nil
	}

	// Fetch segments from Mongo by segmentId string field
	col := s.db.Collection("segments")
	cur, err := col.Find(ctx, bson.M{"segmentId": bson.M{"$in": segIDs}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var segments []models.Segment
	if err := cur.All(ctx, &segments); err != nil {
		return nil, err
	}

	// Track access for heat scoring (background operation, don't fail retrieval if this fails)
	go func() {
		heatScorer := NewHeatScorer(s.db)
		for _, segment := range segments {
			if err := heatScorer.UpdateSegmentAccess(context.Background(), segment.SegmentID); err != nil {
				log.Printf("WARN: Failed to update segment access for heat scoring %s: %v", segment.SegmentID, err)
			}
		}
	}()

	log.Printf("INFO: Retrieved %d segments for query, tracking access for heat scoring", len(segments))
	return segments, nil
}

// GetDialoguePagesByIDs fetches dialogue pages by ObjectIDs
func (s *STMStore) GetDialoguePagesByIDs(ctx context.Context, ids []primitive.ObjectID) ([]*models.DialoguePage, error) {
	if len(ids) == 0 {
		return []*models.DialoguePage{}, nil
	}
	col := s.db.Collection(DialoguePagesCollection)
	cur, err := col.Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var pages []*models.DialoguePage
	if err := cur.All(ctx, &pages); err != nil {
		return nil, err
	}
	return pages, nil
}

// CreateSegmentSummary generates a one-sentence summary for a segment using the LLM
func (s *STMStore) CreateSegmentSummary(ctx context.Context, pages []models.DialoguePage) (string, error) {
	if LLMBaseURL == "" {
		return "", fmt.Errorf("LLM_BASE_URL not configured")
	}
	var b strings.Builder
	b.WriteString("Summarize the following conversation segment in one sentence, focusing on topic and key facts.\n\n")
	for _, p := range pages {
		b.WriteString("User: ")
		b.WriteString(p.UserMessage)
		b.WriteString("\nAssistant: ")
		b.WriteString(p.AgentResponse)
		b.WriteString("\n---\n")
	}
	prompt := b.String()
	modelName := LLMModelName
	if modelName == "" {
		modelName = "Qwen/Qwen3-32B-AWQ"
	}
	reqBody := map[string]interface{}{
		"model":       modelName,
		"prompt":      prompt,
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
	resp, err := (&http.Client{}).Do(req)
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
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &llmResp); err != nil {
		return "", err
	}
	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("empty summary choices")
	}
	return strings.TrimSpace(llmResp.Choices[0].Text), nil
}

// StoreSegmentEmbedding creates an embedding for a segment summary and stores it with tenant scope
func (s *STMStore) StoreSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string, summary string) error {
	if s.milvus == nil {
		return fmt.Errorf("milvus client not configured for segment embeddings")
	}
	emb, err := s.CreateEmbedding(ctx, summary, "")
	if err != nil {
		return fmt.Errorf("failed to create segment embedding: %w", err)
	}
	return s.milvus.InsertSegmentEmbedding(ctx, tenantID, userID, agentID, segmentID, emb)
}

// GetRecentConversationContext retrieves recent dialogue pages for a user
func (s *STMStore) GetRecentConversationContext(ctx context.Context, userID string, limit int) ([]*models.DialoguePage, error) {
	collection := s.db.Collection(DialoguePagesCollection)

	filter := bson.M{
		"userId": userID,
		"status": "in_stm",
	}

	opts := options.Find()
	opts.SetSort(bson.D{bson.E{Key: "createdAt", Value: -1}}) // Most recent first
	opts.SetLimit(int64(limit))

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to find recent pages: %w", err)
	}
	defer cursor.Close(ctx)

	var pages []*models.DialoguePage
	for cursor.Next(ctx) {
		var page models.DialoguePage
		if err := cursor.Decode(&page); err != nil {
			log.Printf("WARN: Failed to decode dialogue page: %v", err)
			continue
		}
		pages = append(pages, &page)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return pages, nil
}

// GetDialogueChainPages retrieves all pages for a specific dialogue chain
func (s *STMStore) GetDialogueChainPages(ctx context.Context, chainID string) ([]*models.DialoguePage, error) {
	collection := s.db.Collection(DialoguePagesCollection)

	filter := bson.M{"chainId": chainID}
	opts := options.Find()
	opts.SetSort(bson.D{bson.E{Key: "createdAt", Value: 1}}) // Chronological order

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to find chain pages: %w", err)
	}
	defer cursor.Close(ctx)

	var pages []*models.DialoguePage
	for cursor.Next(ctx) {
		var page models.DialoguePage
		if err := cursor.Decode(&page); err != nil {
			log.Printf("WARN: Failed to decode dialogue page: %v", err)
			continue
		}
		pages = append(pages, &page)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return pages, nil
}

// Helper functions for retrieval

func (s *STMStore) findSimilarEmbeddings(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, error) {
	// Use Milvus if available
	if s.milvus != nil {
		pageIDs, scores, err := s.milvus.SearchSimilarEmbeddings(ctx, tenantID, userID, agentID, queryVector, limit)
		if err != nil {
			log.Printf("WARN: Milvus search failed, falling back to Redis: %v", err)
			return s.findSimilarEmbeddingsInRedis(ctx, tenantID, userID, agentID, queryVector, limit)
		}

		log.Printf("INFO: Found %d similar embeddings in Milvus with scores: %v", len(pageIDs), scores)
		return pageIDs, nil
	}

	// Fall back to Redis
	return s.findSimilarEmbeddingsInRedis(ctx, tenantID, userID, agentID, queryVector, limit)
}

func (s *STMStore) findSimilarEmbeddingsInRedis(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, error) {
	// This is a simplified similarity search using Redis
	// Get all embedding keys for this user (in practice, this would be optimized)
	pattern := "embedding:*"
	keys, err := s.redis.Keys(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding keys: %w", err)
	}

	type similarity struct {
		pageID string
		score  float64
	}

	var similarities []similarity

	// Calculate cosine similarity for each embedding
	for _, key := range keys {
		var embeddingJSON string
		err := s.redis.Get(key, &embeddingJSON)
		if err != nil {
			continue
		}

		var embedding models.EmbeddingData
		if err := json.Unmarshal([]byte(embeddingJSON), &embedding); err != nil {
			continue
		}

		// Calculate cosine similarity
		score, err := cosineSimilarity(queryVector, embedding.Vector)
		if err != nil {
			log.Printf("WARN: Cosine similarity calculation failed for page %s: %v", embedding.PageID, err)
			continue
		}
		similarities = append(similarities, similarity{
			pageID: embedding.PageID,
			score:  score,
		})
	}

	// Sort by similarity score (highest first)
	for i := 0; i < len(similarities)-1; i++ {
		for j := i + 1; j < len(similarities); j++ {
			if similarities[j].score > similarities[i].score {
				similarities[i], similarities[j] = similarities[j], similarities[i]
			}
		}
	}

	// Return top results
	var pageIDs []string
	maxResults := min(limit, len(similarities))
	for i := 0; i < maxResults; i++ {
		pageIDs = append(pageIDs, similarities[i].pageID)
	}

	log.Printf("INFO: Found %d similar embeddings in Redis (fallback)", len(pageIDs))
	return pageIDs, nil
}

func (s *STMStore) getDialoguePagesByIDs(ctx context.Context, pageIDs []string) ([]*models.DialoguePage, error) {
	if len(pageIDs) == 0 {
		return []*models.DialoguePage{}, nil
	}

	collection := s.db.Collection(DialoguePagesCollection)

	// Convert string IDs to ObjectIDs
	objectIDs := make([]primitive.ObjectID, 0, len(pageIDs))
	for _, pageID := range pageIDs {
		if oid, err := primitive.ObjectIDFromHex(pageID); err == nil {
			objectIDs = append(objectIDs, oid)
		}
	}

	filter := bson.M{"_id": bson.M{"$in": objectIDs}}
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to find pages by IDs: %w", err)
	}
	defer cursor.Close(ctx)

	var pages []*models.DialoguePage
	for cursor.Next(ctx) {
		var page models.DialoguePage
		if err := cursor.Decode(&page); err != nil {
			log.Printf("WARN: Failed to decode dialogue page: %v", err)
			continue
		}
		pages = append(pages, &page)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return pages, nil
}

// Utility functions

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
