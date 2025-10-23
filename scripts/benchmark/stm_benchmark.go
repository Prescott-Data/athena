package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BenchmarkConfig holds configuration for STM benchmarking
type BenchmarkConfig struct {
	TestUsers         int           `json:"test_users"`
	ConversationsPerUser int        `json:"conversations_per_user"`
	TurnsPerConversation int        `json:"turns_per_conversation"`
	ConcurrentWorkers    int        `json:"concurrent_workers"`
	TestDurationMinutes  int        `json:"test_duration_minutes"`
	IncludeEmbeddings    bool       `json:"include_embeddings"`
	TestGuardrails       bool       `json:"test_guardrails"`
	CleanupAfter         bool       `json:"cleanup_after"`
}

// BenchmarkResults holds the results of STM benchmarking
type BenchmarkResults struct {
	SystemName               string                 `json:"system_name"`
	StartTime                time.Time              `json:"start_time"`
	EndTime                  time.Time              `json:"end_time"`
	TotalDuration            time.Duration          `json:"total_duration"`
	
	// Operation Metrics
	CacheOperations          OperationMetrics       `json:"cache_operations"`
	DialogueChainDecisions   OperationMetrics       `json:"dialogue_chain_decisions"`
	EmbeddingOperations      OperationMetrics       `json:"embedding_operations"`
	MongoOperations          OperationMetrics       `json:"mongo_operations"`
	QualityValidations       OperationMetrics       `json:"quality_validations"`
	
	// Performance Metrics
	AverageLatency           map[string]time.Duration `json:"average_latency"`
	ThroughputOpsPerSecond   map[string]float64      `json:"throughput_ops_per_second"`
	ErrorRates               map[string]float64      `json:"error_rates"`
	
	// Resource Metrics
	PeakMemoryUsageMB        float64                `json:"peak_memory_usage_mb"`
	AverageMemoryUsageMB     float64                `json:"average_memory_usage_mb"`
	CPUUsagePercent          float64                `json:"cpu_usage_percent"`
	
	// Cache Metrics
	CacheHitRate             float64                `json:"cache_hit_rate"`
	CacheMissRate            float64                `json:"cache_miss_rate"`
	
	// Guardrails Metrics
	RateLimitTriggers        int                    `json:"rate_limit_triggers"`
	CircuitBreakerTriggers   int                    `json:"circuit_breaker_triggers"`
	
	// Quality Metrics
	QualityScoreDistribution map[string]int         `json:"quality_score_distribution"`
	ValidationModeResults    map[string]float64     `json:"validation_mode_results"`
}

// OperationMetrics tracks metrics for a specific operation type
type OperationMetrics struct {
	TotalOperations  int           `json:"total_operations"`
	SuccessfulOps    int           `json:"successful_ops"`
	FailedOps        int           `json:"failed_ops"`
	AverageLatency   time.Duration `json:"average_latency"`
	MinLatency       time.Duration `json:"min_latency"`
	MaxLatency       time.Duration `json:"max_latency"`
	P95Latency       time.Duration `json:"p95_latency"`
	P99Latency       time.Duration `json:"p99_latency"`
	Throughput       float64       `json:"throughput_ops_per_second"`
}

// STMBenchmark manages STM performance testing
type STMBenchmark struct {
	config      BenchmarkConfig
	stmCache    *memory.STMCache
	stmStore    *memory.STMStore
	redis       cache.Interface
	mongodb     *mongo.Database
	results     *BenchmarkResults
	
	// Tracking
	operationLatencies map[string][]time.Duration
	operationCounts    map[string]int
	errorCounts        map[string]int
	memoryUsage        []float64
	mutex              sync.RWMutex
}

// NewSTMBenchmark creates a new STM benchmark instance
func NewSTMBenchmark(config BenchmarkConfig) (*STMBenchmark, error) {
	// Initialize Redis
	redisClient, err := cache.NewRedisClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}
	
	// Initialize MongoDB
	mongoConfig := &database.ConnectionConfig{
		URI:      os.Getenv("MONGO_URI"),
		Database: os.Getenv("MONGO_DATABASE"),
	}
	
	mongoClient, err := database.ConnectMongoDB(mongoConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	
	mongodb := database.GetDatabase()
	
	// Initialize STM components
	stmCache := memory.NewSTMCache(redisClient)
	stmStore := memory.NewSTMStore(mongodb, redisClient)
	
	// Initialize benchmark
	benchmark := &STMBenchmark{
		config:             config,
		stmCache:           stmCache,
		stmStore:           stmStore,
		redis:              redisClient,
		mongodb:            mongodb,
		operationLatencies: make(map[string][]time.Duration),
		operationCounts:    make(map[string]int),
		errorCounts:        make(map[string]int),
		memoryUsage:        make([]float64, 0),
		results: &BenchmarkResults{
			AverageLatency:         make(map[string]time.Duration),
			ThroughputOpsPerSecond: make(map[string]float64),
			ErrorRates:             make(map[string]float64),
			QualityScoreDistribution: make(map[string]int),
			ValidationModeResults:    make(map[string]float64),
		},
	}
	
	return benchmark, nil
}

// RunBenchmark executes the complete STM benchmark suite
func (b *STMBenchmark) RunBenchmark(ctx context.Context) (*BenchmarkResults, error) {
	log.Printf("INFO: Starting STM benchmark - Users: %d, Conversations: %d, Concurrent: %d", 
		b.config.TestUsers, b.config.ConversationsPerUser, b.config.ConcurrentWorkers)
	
	b.results.SystemName = "Memory-OS STM"
	b.results.StartTime = time.Now()
	
	// Start memory monitoring
	memCtx, memCancel := context.WithCancel(ctx)
	go b.monitorMemoryUsage(memCtx)
	
	// Run benchmark phases
	if err := b.runCacheBenchmark(ctx); err != nil {
		return nil, fmt.Errorf("cache benchmark failed: %w", err)
	}
	
	if err := b.runDialogueChainBenchmark(ctx); err != nil {
		return nil, fmt.Errorf("dialogue chain benchmark failed: %w", err)
	}
	
	if b.config.IncludeEmbeddings {
		if err := b.runEmbeddingBenchmark(ctx); err != nil {
			return nil, fmt.Errorf("embedding benchmark failed: %w", err)
		}
	}
	
	if err := b.runQualityValidationBenchmark(ctx); err != nil {
		return nil, fmt.Errorf("quality validation benchmark failed: %w", err)
	}
	
	if b.config.TestGuardrails {
		if err := b.runGuardrailsBenchmark(ctx); err != nil {
			return nil, fmt.Errorf("guardrails benchmark failed: %w", err)
		}
	}
	
	// Stop memory monitoring and finalize results
	memCancel()
	b.results.EndTime = time.Now()
	b.results.TotalDuration = b.results.EndTime.Sub(b.results.StartTime)
	
	b.calculateFinalMetrics()
	
	// Cleanup if requested
	if b.config.CleanupAfter {
		if err := b.cleanup(ctx); err != nil {
			log.Printf("WARN: Cleanup failed: %v", err)
		}
	}
	
	log.Printf("INFO: STM benchmark completed in %v", b.results.TotalDuration)
	return b.results, nil
}

// runCacheBenchmark tests STM cache operations
func (b *STMBenchmark) runCacheBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running STM cache benchmark...")
	
	var wg sync.WaitGroup
	userChan := make(chan string, b.config.TestUsers)
	
	// Generate user IDs
	for i := 0; i < b.config.TestUsers; i++ {
		userChan <- fmt.Sprintf("user_%d", i)
	}
	close(userChan)
	
	// Start concurrent workers
	for i := 0; i < b.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for userID := range userChan {
				b.benchmarkUserCacheOperations(ctx, userID)
			}
		}()
	}
	
	wg.Wait()
	log.Printf("INFO: STM cache benchmark completed")
	return nil
}

// benchmarkUserCacheOperations tests cache operations for a specific user
func (b *STMBenchmark) benchmarkUserCacheOperations(ctx context.Context, userID string) {
	for i := 0; i < b.config.ConversationsPerUser; i++ {
		for j := 0; j < b.config.TurnsPerConversation; j++ {
			// Create test conversation turn
			turn := &models.ConversationTurn{
				UserMessage:   fmt.Sprintf("Test message %d:%d from %s", i, j, userID),
				AgentResponse: fmt.Sprintf("Test response %d:%d for %s", i, j, userID),
				Timestamp:     time.Now(),
				Metadata:      map[string]interface{}{"test": true, "turn": j},
			}
			
			// Test cache store operation
			start := time.Now()
			err := b.stmCache.AddConversationTurn(ctx, userID, turn)
			latency := time.Since(start)
			
			b.recordOperation("cache_store", latency, err == nil)
			
			// Test cache retrieve operation
			start = time.Now()
			context, err := b.stmCache.GetConversationContext(ctx, userID, 10)
			latency = time.Since(start)
			
			b.recordOperation("cache_retrieve", latency, err == nil && context != nil)
			
			// Test cache stats
			start = time.Now()
			_, err = b.stmCache.GetCacheStats(ctx, userID)
			latency = time.Since(start)
			
			b.recordOperation("cache_stats", latency, err == nil)
		}
	}
}

// runDialogueChainBenchmark tests dialogue chain decision logic
func (b *STMBenchmark) runDialogueChainBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running dialogue chain decision benchmark...")
	
	var wg sync.WaitGroup
	testCases := b.generateDialogueChainTestCases()
	
	caseChan := make(chan DialogueChainTestCase, len(testCases))
	for _, testCase := range testCases {
		caseChan <- testCase
	}
	close(caseChan)
	
	// Start concurrent workers
	for i := 0; i < b.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for testCase := range caseChan {
				b.benchmarkDialogueChainDecision(ctx, testCase)
			}
		}()
	}
	
	wg.Wait()
	log.Printf("INFO: Dialogue chain decision benchmark completed")
	return nil
}

// DialogueChainTestCase represents a test case for dialogue chain decisions
type DialogueChainTestCase struct {
	UserID           string
	PreviousMessage  string
	PreviousResponse string
	CurrentMessage   string
	CurrentResponse  string
	ExpectedResult   bool
}

// generateDialogueChainTestCases creates test cases for dialogue chain decisions
func (b *STMBenchmark) generateDialogueChainTestCases() []DialogueChainTestCase {
	testCases := make([]DialogueChainTestCase, 0, 100)
	
	// High similarity cases (should continue)
	for i := 0; i < 25; i++ {
		testCases = append(testCases, DialogueChainTestCase{
			UserID:           fmt.Sprintf("user_%d", rand.Intn(b.config.TestUsers)),
			PreviousMessage:  "How do I configure Redis settings?",
			PreviousResponse: "You can configure Redis by editing the redis.conf file...",
			CurrentMessage:   "What about the timeout settings in Redis?",
			CurrentResponse:  "Redis timeout settings can be configured using the timeout parameter...",
			ExpectedResult:   true,
		})
	}
	
	// Low similarity cases (should start new chain)
	for i := 0; i < 25; i++ {
		testCases = append(testCases, DialogueChainTestCase{
			UserID:           fmt.Sprintf("user_%d", rand.Intn(b.config.TestUsers)),
			PreviousMessage:  "How do I configure Redis settings?",
			PreviousResponse: "You can configure Redis by editing the redis.conf file...",
			CurrentMessage:   "What's the weather like today?",
			CurrentResponse:  "I don't have access to current weather information...",
			ExpectedResult:   false,
		})
	}
	
	// Gray zone cases (moderate similarity)
	for i := 0; i < 50; i++ {
		testCases = append(testCases, DialogueChainTestCase{
			UserID:           fmt.Sprintf("user_%d", rand.Intn(b.config.TestUsers)),
			PreviousMessage:  "How do I configure database settings?",
			PreviousResponse: "Database configuration depends on your specific database system...",
			CurrentMessage:   "What about performance optimization?",
			CurrentResponse:  "Performance optimization techniques vary based on your use case...",
			ExpectedResult:   rand.Float64() > 0.5, // Mixed expected results
		})
	}
	
	return testCases
}

// benchmarkDialogueChainDecision tests dialogue chain decision for a test case
func (b *STMBenchmark) benchmarkDialogueChainDecision(ctx context.Context, testCase DialogueChainTestCase) {
	start := time.Now()
	
	chainID, err := b.stmStore.DetermineDialogueChain(
		ctx,
		testCase.UserID,
		testCase.CurrentMessage,
		testCase.CurrentResponse,
	)
	
	latency := time.Since(start)
	success := err == nil && chainID != ""
	
	b.recordOperation("dialogue_chain_decision", latency, success)
}

// runEmbeddingBenchmark tests embedding operations
func (b *STMBenchmark) runEmbeddingBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running embedding benchmark...")
	
	testTexts := []string{
		"How do I configure Redis for high availability?",
		"What are the best practices for MongoDB indexing?",
		"How can I optimize my application's performance?",
		"What's the difference between SQL and NoSQL databases?",
		"How do I implement authentication in my web application?",
	}
	
	var wg sync.WaitGroup
	
	// Test embedding creation
	for _, text := range testTexts {
		for i := 0; i < 10; i++ { // Multiple iterations per text
			wg.Add(1)
			go func(text string) {
				defer wg.Done()
				b.benchmarkEmbeddingOperation(ctx, text)
			}(text)
		}
	}
	
	wg.Wait()
	log.Printf("INFO: Embedding benchmark completed")
	return nil
}

// benchmarkEmbeddingOperation tests embedding creation and storage
func (b *STMBenchmark) benchmarkEmbeddingOperation(ctx context.Context, text string) {
	pageID := fmt.Sprintf("page_%d", rand.Int63())
	
	// Test embedding creation
	start := time.Now()
	embedding, err := b.stmStore.CreateEmbedding(ctx, text)
	latency := time.Since(start)
	
	b.recordOperation("embedding_create", latency, err == nil && embedding != nil)
	
	if embedding != nil {
		// Test embedding storage
		start = time.Now()
		err = b.stmStore.StoreEmbedding(ctx, pageID, embedding)
		latency = time.Since(start)
		
		b.recordOperation("embedding_store", latency, err == nil)
		
		// Test embedding retrieval
		start = time.Now()
		retrieved, err := b.stmStore.GetEmbedding(ctx, pageID)
		latency = time.Since(start)
		
		b.recordOperation("embedding_retrieve", latency, err == nil && retrieved != nil)
	}
}

// runQualityValidationBenchmark tests quality validation
func (b *STMBenchmark) runQualityValidationBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running quality validation benchmark...")
	
	testSegments := b.generateQualityTestSegments()
	validator := memory.NewQualityValidator()
	
	var wg sync.WaitGroup
	segmentChan := make(chan *models.Segment, len(testSegments))
	
	for _, segment := range testSegments {
		segmentChan <- segment
	}
	close(segmentChan)
	
	// Start concurrent workers
	for i := 0; i < b.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for segment := range segmentChan {
				b.benchmarkQualityValidation(ctx, validator, segment)
			}
		}()
	}
	
	wg.Wait()
	log.Printf("INFO: Quality validation benchmark completed")
	return nil
}

// generateQualityTestSegments creates test segments for quality validation
func (b *STMBenchmark) generateQualityTestSegments() []*models.Segment {
	segments := make([]*models.Segment, 0, 100)
	
	// High quality segments
	for i := 0; i < 30; i++ {
		segments = append(segments, &models.Segment{
			SegmentID:    fmt.Sprintf("high_quality_%d", i),
			UserID:       fmt.Sprintf("user_%d", rand.Intn(10)),
			ChainID:      fmt.Sprintf("chain_%d", rand.Intn(5)),
			TopicSummary: "Detailed discussion about Redis configuration, performance optimization, and best practices for production deployment with specific examples and troubleshooting steps.",
			Status:       "active",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		})
	}
	
	// Medium quality segments
	for i := 0; i < 40; i++ {
		segments = append(segments, &models.Segment{
			SegmentID:    fmt.Sprintf("medium_quality_%d", i),
			UserID:       fmt.Sprintf("user_%d", rand.Intn(10)),
			ChainID:      fmt.Sprintf("chain_%d", rand.Intn(5)),
			TopicSummary: "Basic question about database setup with some context.",
			Status:       "active",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		})
	}
	
	// Low quality segments
	for i := 0; i < 30; i++ {
		segments = append(segments, &models.Segment{
			SegmentID:    fmt.Sprintf("low_quality_%d", i),
			UserID:       fmt.Sprintf("user_%d", rand.Intn(10)),
			ChainID:      fmt.Sprintf("chain_%d", rand.Intn(5)),
			TopicSummary: "Hi",
			Status:       "active",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		})
	}
	
	return segments
}

// benchmarkQualityValidation tests quality validation for a segment
func (b *STMBenchmark) benchmarkQualityValidation(ctx context.Context, validator *memory.QualityValidator, segment *models.Segment) {
	// Generate fake dialogue pages for the segment
	pages := []models.DialoguePage{
		{
			UserMessage:   "How do I optimize my Redis configuration?",
			AgentResponse: "Redis optimization depends on your specific use case. Here are key areas to focus on...",
			TurnIndex:     0,
			CreatedAt:     time.Now(),
		},
		{
			UserMessage:   "What about memory management?",
			AgentResponse: "Redis memory management involves several strategies including eviction policies, memory optimization techniques...",
			TurnIndex:     1,
			CreatedAt:     time.Now(),
		},
	}
	
	start := time.Now()
	result, err := validator.ValidateSegment(ctx, segment, pages)
	latency := time.Since(start)
	
	success := err == nil && result != nil
	b.recordOperation("quality_validation", latency, success)
	
	if result != nil {
		// Record quality score distribution
		scoreRange := fmt.Sprintf("%.1f-%.1f", 
			float64(int(result.QualityScore*10))/10, 
			float64(int(result.QualityScore*10)+1)/10)
		
		b.mutex.Lock()
		b.results.QualityScoreDistribution[scoreRange]++
		b.mutex.Unlock()
	}
}

// runGuardrailsBenchmark tests rate limiting and circuit breaker
func (b *STMBenchmark) runGuardrailsBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running guardrails benchmark...")
	
	// Test rate limiting
	userID := "rate_limit_test_user"
	
	// Make rapid requests to trigger rate limiting
	for i := 0; i < 100; i++ {
		start := time.Now()
		_, err := b.stmStore.DetermineDialogueChain(ctx, userID, "test message", "test response")
		latency := time.Since(start)
		
		if err != nil && (err.Error() == "rate limit exceeded" || err.Error() == "circuit breaker open") {
			b.mutex.Lock()
			if err.Error() == "rate limit exceeded" {
				b.results.RateLimitTriggers++
			} else {
				b.results.CircuitBreakerTriggers++
			}
			b.mutex.Unlock()
		}
		
		b.recordOperation("guardrails_test", latency, err == nil)
		
		// Small delay between requests
		time.Sleep(10 * time.Millisecond)
	}
	
	log.Printf("INFO: Guardrails benchmark completed")
	return nil
}

// monitorMemoryUsage continuously monitors memory usage
func (b *STMBenchmark) monitorMemoryUsage(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			
			memoryMB := float64(m.Alloc) / 1024 / 1024
			
			b.mutex.Lock()
			b.memoryUsage = append(b.memoryUsage, memoryMB)
			b.mutex.Unlock()
		}
	}
}

// recordOperation records metrics for an operation
func (b *STMBenchmark) recordOperation(operation string, latency time.Duration, success bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	
	b.operationCounts[operation]++
	
	if b.operationLatencies[operation] == nil {
		b.operationLatencies[operation] = make([]time.Duration, 0)
	}
	b.operationLatencies[operation] = append(b.operationLatencies[operation], latency)
	
	if !success {
		b.errorCounts[operation]++
	}
}

// calculateFinalMetrics calculates final benchmark metrics
func (b *STMBenchmark) calculateFinalMetrics() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	
	// Calculate memory metrics
	if len(b.memoryUsage) > 0 {
		var sum float64
		var peak float64
		
		for _, usage := range b.memoryUsage {
			sum += usage
			if usage > peak {
				peak = usage
			}
		}
		
		b.results.AverageMemoryUsageMB = sum / float64(len(b.memoryUsage))
		b.results.PeakMemoryUsageMB = peak
	}
	
	// Calculate operation metrics
	totalDurationSeconds := b.results.TotalDuration.Seconds()
	
	for operation, latencies := range b.operationLatencies {
		if len(latencies) == 0 {
			continue
		}
		
		// Calculate average latency
		var sum time.Duration
		for _, latency := range latencies {
			sum += latency
		}
		avgLatency := sum / time.Duration(len(latencies))
		b.results.AverageLatency[operation] = avgLatency
		
		// Calculate throughput
		b.results.ThroughputOpsPerSecond[operation] = float64(len(latencies)) / totalDurationSeconds
		
		// Calculate error rate
		errorCount := b.errorCounts[operation]
		b.results.ErrorRates[operation] = float64(errorCount) / float64(len(latencies)) * 100
	}
	
	// Calculate cache hit rate (simplified)
	cacheHits := b.operationCounts["cache_retrieve"] - b.errorCounts["cache_retrieve"]
	if b.operationCounts["cache_retrieve"] > 0 {
		b.results.CacheHitRate = float64(cacheHits) / float64(b.operationCounts["cache_retrieve"]) * 100
		b.results.CacheMissRate = 100 - b.results.CacheHitRate
	}
}

// cleanup removes test data
func (b *STMBenchmark) cleanup(ctx context.Context) error {
	log.Printf("INFO: Cleaning up test data...")
	
	// Clear Redis test data
	for i := 0; i < b.config.TestUsers; i++ {
		userID := fmt.Sprintf("user_%d", i)
		b.redis.DeletePattern(fmt.Sprintf("stm:*:%s", userID))
	}
	
	// Clear test MongoDB collections (if needed)
	// Note: Be careful with this in production environments
	
	return nil
}

// PrintResults prints benchmark results in a formatted way
func (b *STMBenchmark) PrintResults() {
	results := b.results
	
	fmt.Printf("\n" + strings.Repeat("=", 80) + "\n")
	fmt.Printf("STM BENCHMARK RESULTS - %s\n", results.SystemName)
	fmt.Printf(strings.Repeat("=", 80) + "\n")
	
	fmt.Printf("Test Duration: %v\n", results.TotalDuration)
	fmt.Printf("Peak Memory Usage: %.2f MB\n", results.PeakMemoryUsageMB)
	fmt.Printf("Average Memory Usage: %.2f MB\n", results.AverageMemoryUsageMB)
	fmt.Printf("Cache Hit Rate: %.2f%%\n", results.CacheHitRate)
	
	fmt.Printf("\nOperation Performance:\n")
	fmt.Printf("%-25s %-15s %-15s %-10s\n", "Operation", "Avg Latency", "Throughput/s", "Error %")
	fmt.Printf(strings.Repeat("-", 80) + "\n")
	
	for operation, latency := range results.AverageLatency {
		throughput := results.ThroughputOpsPerSecond[operation]
		errorRate := results.ErrorRates[operation]
		
		fmt.Printf("%-25s %-15v %-15.2f %-10.2f\n", 
			operation, latency, throughput, errorRate)
	}
	
	if results.RateLimitTriggers > 0 || results.CircuitBreakerTriggers > 0 {
		fmt.Printf("\nGuardrails:\n")
		fmt.Printf("Rate Limit Triggers: %d\n", results.RateLimitTriggers)
		fmt.Printf("Circuit Breaker Triggers: %d\n", results.CircuitBreakerTriggers)
	}
	
	fmt.Printf("\nQuality Score Distribution:\n")
	for scoreRange, count := range results.QualityScoreDistribution {
		fmt.Printf("  %s: %d segments\n", scoreRange, count)
	}
	
	fmt.Printf(strings.Repeat("=", 80) + "\n")
}

// SaveResults saves benchmark results to JSON file
func (b *STMBenchmark) SaveResults(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create results file: %w", err)
	}
	defer file.Close()
	
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	
	if err := encoder.Encode(b.results); err != nil {
		return fmt.Errorf("failed to encode results: %w", err)
	}
	
	log.Printf("INFO: Benchmark results saved to %s", filename)
	return nil
}

func main() {
	// Default benchmark configuration
	config := BenchmarkConfig{
		TestUsers:            10,
		ConversationsPerUser: 5,
		TurnsPerConversation: 3,
		ConcurrentWorkers:    4,
		TestDurationMinutes:  5,
		IncludeEmbeddings:    true,
		TestGuardrails:       true,
		CleanupAfter:         true,
	}
	
	// Override from environment if provided
	if os.Getenv("BENCHMARK_USERS") != "" {
		if users, err := strconv.Atoi(os.Getenv("BENCHMARK_USERS")); err == nil {
			config.TestUsers = users
		}
	}
	
	if os.Getenv("BENCHMARK_WORKERS") != "" {
		if workers, err := strconv.Atoi(os.Getenv("BENCHMARK_WORKERS")); err == nil {
			config.ConcurrentWorkers = workers
		}
	}
	
	// Create and run benchmark
	benchmark, err := NewSTMBenchmark(config)
	if err != nil {
		log.Fatalf("Failed to create benchmark: %v", err)
	}
	
	ctx := context.Background()
	results, err := benchmark.RunBenchmark(ctx)
	if err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}
	
	// Print and save results
	benchmark.PrintResults()
	
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("stm_benchmark_results_%s.json", timestamp)
	
	if err := benchmark.SaveResults(filename); err != nil {
		log.Printf("Failed to save results: %v", err)
	}
	
	fmt.Printf("\nBenchmark completed successfully!\n")
}
