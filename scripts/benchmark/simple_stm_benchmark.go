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
)

// Simplified benchmark focusing on core STM operations
// This version tests the key functionality without complex dependencies

// BenchmarkConfig holds configuration for STM benchmarking
type BenchmarkConfig struct {
	TestUsers            int  `json:"test_users"`
	ConversationsPerUser int  `json:"conversations_per_user"`
	TurnsPerConversation int  `json:"turns_per_conversation"`
	ConcurrentWorkers    int  `json:"concurrent_workers"`
	TestDurationMinutes  int  `json:"test_duration_minutes"`
	CleanupAfter         bool `json:"cleanup_after"`
}

// BenchmarkResults holds the results of STM benchmarking
type BenchmarkResults struct {
	SystemName    string        `json:"system_name"`
	StartTime     time.Time     `json:"start_time"`
	EndTime       time.Time     `json:"end_time"`
	TotalDuration time.Duration `json:"total_duration"`

	// Operation Metrics
	CacheOperations        OperationMetrics `json:"cache_operations"`
	DialogueChainDecisions OperationMetrics `json:"dialogue_chain_decisions"`
	QualityValidations     OperationMetrics `json:"quality_validations"`

	// Performance Metrics
	AverageLatency         map[string]time.Duration `json:"average_latency"`
	ThroughputOpsPerSecond map[string]float64       `json:"throughput_ops_per_second"`
	ErrorRates             map[string]float64       `json:"error_rates"`

	// Resource Metrics
	PeakMemoryUsageMB    float64 `json:"peak_memory_usage_mb"`
	AverageMemoryUsageMB float64 `json:"average_memory_usage_mb"`

	// Cache Metrics
	CacheHitRate  float64 `json:"cache_hit_rate"`
	CacheMissRate float64 `json:"cache_miss_rate"`

	// Quality Metrics
	QualityScoreDistribution map[string]int `json:"quality_score_distribution"`
}

// OperationMetrics tracks metrics for a specific operation type
type OperationMetrics struct {
	TotalOperations int           `json:"total_operations"`
	SuccessfulOps   int           `json:"successful_ops"`
	FailedOps       int           `json:"failed_ops"`
	AverageLatency  time.Duration `json:"average_latency"`
	MinLatency      time.Duration `json:"min_latency"`
	MaxLatency      time.Duration `json:"max_latency"`
	Throughput      float64       `json:"throughput_ops_per_second"`
}

// STMBenchmark manages STM performance testing
type STMBenchmark struct {
	config  BenchmarkConfig
	results *BenchmarkResults

	// Tracking
	operationLatencies map[string][]time.Duration
	operationCounts    map[string]int
	errorCounts        map[string]int
	memoryUsage        []float64
	mutex              sync.RWMutex

	// Mock components for testing
	cache      map[string]interface{}
	cacheMutex sync.RWMutex
}

// MockConversationTurn represents a conversation turn for testing
type MockConversationTurn struct {
	UserMessage   string                 `json:"user_message"`
	AgentResponse string                 `json:"agent_response"`
	Timestamp     time.Time              `json:"timestamp"`
	Metadata      map[string]interface{} `json:"metadata"`
}

// MockSegment represents a segment for testing
type MockSegment struct {
	SegmentID    string    `json:"segment_id"`
	UserID       string    `json:"user_id"`
	ChainID      string    `json:"chain_id"`
	TopicSummary string    `json:"topic_summary"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NewSTMBenchmark creates a new STM benchmark instance
func NewSTMBenchmark(config BenchmarkConfig) (*STMBenchmark, error) {
	benchmark := &STMBenchmark{
		config:             config,
		operationLatencies: make(map[string][]time.Duration),
		operationCounts:    make(map[string]int),
		errorCounts:        make(map[string]int),
		memoryUsage:        make([]float64, 0),
		cache:              make(map[string]interface{}),
		results: &BenchmarkResults{
			AverageLatency:           make(map[string]time.Duration),
			ThroughputOpsPerSecond:   make(map[string]float64),
			ErrorRates:               make(map[string]float64),
			QualityScoreDistribution: make(map[string]int),
		},
	}

	return benchmark, nil
}

// RunBenchmark executes the complete STM benchmark suite
func (b *STMBenchmark) RunBenchmark(ctx context.Context) (*BenchmarkResults, error) {
	log.Printf("INFO: Starting simplified STM benchmark - Users: %d, Conversations: %d, Concurrent: %d",
		b.config.TestUsers, b.config.ConversationsPerUser, b.config.ConcurrentWorkers)

	b.results.SystemName = "Memory-OS STM (Simplified)"
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

	if err := b.runQualityValidationBenchmark(ctx); err != nil {
		return nil, fmt.Errorf("quality validation benchmark failed: %w", err)
	}

	// Stop memory monitoring and finalize results
	memCancel()
	b.results.EndTime = time.Now()
	b.results.TotalDuration = b.results.EndTime.Sub(b.results.StartTime)

	b.calculateFinalMetrics()

	log.Printf("INFO: STM benchmark completed in %v", b.results.TotalDuration)
	return b.results, nil
}

// runCacheBenchmark tests STM cache operations with mock cache
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
			turn := &MockConversationTurn{
				UserMessage:   fmt.Sprintf("Test message %d:%d from %s", i, j, userID),
				AgentResponse: fmt.Sprintf("Test response %d:%d for %s", i, j, userID),
				Timestamp:     time.Now(),
				Metadata:      map[string]interface{}{"test": true, "turn": j},
			}

			// Test cache store operation
			start := time.Now()
			err := b.mockCacheStore(userID, turn)
			latency := time.Since(start)

			b.recordOperation("cache_store", latency, err == nil)

			// Test cache retrieve operation
			start = time.Now()
			context, err := b.mockCacheRetrieve(userID, 10)
			latency = time.Since(start)

			b.recordOperation("cache_retrieve", latency, err == nil && context != nil)

			// Test cache stats
			start = time.Now()
			_, err = b.mockCacheStats(userID)
			latency = time.Since(start)

			b.recordOperation("cache_stats", latency, err == nil)
		}
	}
}

// mockCacheStore simulates storing a conversation turn in cache
func (b *STMBenchmark) mockCacheStore(userID string, turn *MockConversationTurn) error {
	key := fmt.Sprintf("stm:%s", userID)

	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()

	if existing, exists := b.cache[key]; exists {
		if turns, ok := existing.([]*MockConversationTurn); ok {
			b.cache[key] = append(turns, turn)
		}
	} else {
		b.cache[key] = []*MockConversationTurn{turn}
	}

	// Simulate some processing time
	time.Sleep(time.Microsecond * time.Duration(rand.Intn(100)))

	return nil
}

// mockCacheRetrieve simulates retrieving conversation context from cache
func (b *STMBenchmark) mockCacheRetrieve(userID string, limit int) ([]*MockConversationTurn, error) {
	key := fmt.Sprintf("stm:%s", userID)

	b.cacheMutex.RLock()
	defer b.cacheMutex.RUnlock()

	if existing, exists := b.cache[key]; exists {
		if turns, ok := existing.([]*MockConversationTurn); ok {
			// Simulate some processing time
			time.Sleep(time.Microsecond * time.Duration(rand.Intn(50)))

			// Return last N turns
			start := len(turns) - limit
			if start < 0 {
				start = 0
			}
			return turns[start:], nil
		}
	}

	return nil, fmt.Errorf("cache miss")
}

// mockCacheStats simulates getting cache statistics
func (b *STMBenchmark) mockCacheStats(userID string) (map[string]interface{}, error) {
	key := fmt.Sprintf("stm:%s", userID)

	b.cacheMutex.RLock()
	defer b.cacheMutex.RUnlock()

	stats := map[string]interface{}{
		"user_id": userID,
		"turns":   0,
	}

	if existing, exists := b.cache[key]; exists {
		if turns, ok := existing.([]*MockConversationTurn); ok {
			stats["turns"] = len(turns)
		}
	}

	// Simulate some processing time
	time.Sleep(time.Microsecond * time.Duration(rand.Intn(20)))

	return stats, nil
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

	// Mock dialogue chain decision with similarity calculation
	similarity := b.mockCalculateSimilarity(testCase.PreviousMessage, testCase.CurrentMessage)
	chainID := ""

	if similarity > 0.6 {
		chainID = fmt.Sprintf("chain_%s_%d", testCase.UserID, rand.Intn(5))
	} else {
		chainID = fmt.Sprintf("new_chain_%s_%d", testCase.UserID, time.Now().UnixNano())
	}

	// Simulate some processing time
	time.Sleep(time.Millisecond * time.Duration(rand.Intn(20)))

	latency := time.Since(start)
	success := chainID != ""

	b.recordOperation("dialogue_chain_decision", latency, success)
}

// mockCalculateSimilarity simulates calculating similarity between messages
func (b *STMBenchmark) mockCalculateSimilarity(msg1, msg2 string) float64 {
	// Simple mock similarity based on common words
	words1 := strings.Fields(strings.ToLower(msg1))
	words2 := strings.Fields(strings.ToLower(msg2))

	common := 0
	for _, w1 := range words1 {
		for _, w2 := range words2 {
			if w1 == w2 {
				common++
				break
			}
		}
	}

	total := len(words1) + len(words2)
	if total == 0 {
		return 0.0
	}

	return float64(common*2) / float64(total)
}

// runQualityValidationBenchmark tests quality validation
func (b *STMBenchmark) runQualityValidationBenchmark(ctx context.Context) error {
	log.Printf("INFO: Running quality validation benchmark...")

	testSegments := b.generateQualityTestSegments()

	var wg sync.WaitGroup
	segmentChan := make(chan *MockSegment, len(testSegments))

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
				b.benchmarkQualityValidation(ctx, segment)
			}
		}()
	}

	wg.Wait()
	log.Printf("INFO: Quality validation benchmark completed")
	return nil
}

// generateQualityTestSegments creates test segments for quality validation
func (b *STMBenchmark) generateQualityTestSegments() []*MockSegment {
	segments := make([]*MockSegment, 0, 100)

	// High quality segments
	for i := 0; i < 30; i++ {
		segments = append(segments, &MockSegment{
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
		segments = append(segments, &MockSegment{
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
		segments = append(segments, &MockSegment{
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
func (b *STMBenchmark) benchmarkQualityValidation(ctx context.Context, segment *MockSegment) {
	start := time.Now()

	// Mock quality validation
	qualityScore := b.mockCalculateQualityScore(segment)

	// Simulate some processing time
	time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

	latency := time.Since(start)
	success := qualityScore >= 0.0

	b.recordOperation("quality_validation", latency, success)

	// Record quality score distribution
	scoreRange := fmt.Sprintf("%.1f-%.1f",
		float64(int(qualityScore*10))/10,
		float64(int(qualityScore*10)+1)/10)

	b.mutex.Lock()
	b.results.QualityScoreDistribution[scoreRange]++
	b.mutex.Unlock()
}

// mockCalculateQualityScore simulates quality score calculation
func (b *STMBenchmark) mockCalculateQualityScore(segment *MockSegment) float64 {
	score := 0.0

	// Length-based scoring
	summaryLength := len(segment.TopicSummary)
	if summaryLength > 100 {
		score += 0.4
	} else if summaryLength > 50 {
		score += 0.3
	} else if summaryLength > 10 {
		score += 0.2
	} else {
		score += 0.1
	}

	// Topic complexity scoring
	words := strings.Fields(segment.TopicSummary)
	complexWords := 0
	for _, word := range words {
		if len(word) > 6 {
			complexWords++
		}
	}

	if len(words) > 0 {
		complexity := float64(complexWords) / float64(len(words))
		score += complexity * 0.3
	}

	// Random variation to simulate realistic scoring
	score += (rand.Float64() - 0.5) * 0.2

	// Clamp to [0, 1]
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return score
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
		TestDurationMinutes:  2,
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

	log.Printf("INFO: Starting STM benchmark with config: %+v", config)

	// Create and run benchmark
	benchmark, err := NewSTMBenchmark(config)
	if err != nil {
		log.Fatalf("Failed to create benchmark: %v", err)
	}

	ctx := context.Background()
	_, err = benchmark.RunBenchmark(ctx)
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
