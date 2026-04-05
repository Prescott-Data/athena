package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/joho/godotenv/autoload"
)

// Benchmark results structure
type BenchmarkResult struct {
	TotalOperations int           `json:"total_operations"`
	TotalDuration   time.Duration `json:"total_duration"`
	AvgLatency      float64       `json:"avg_latency_ms"`
	Throughput      float64       `json:"throughput_ops_per_sec"`
}

// STMBenchmark holds components for running STM benchmarks
type STMBenchmark struct {
	stmCache *memory.STMCache
	ctx      context.Context
}

// NewSTMBenchmark creates a new benchmark instance
func NewSTMBenchmark() (*STMBenchmark, error) {
	_ = autoload.Load()

	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		return nil, fmt.Errorf("REDIS_HOST not set")
	}

	redisClient, err := cache.NewRedisClient(redisHost, os.Getenv("REDIS_PORT"), os.Getenv("REDIS_PASSWORD"), 2)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &STMBenchmark{
		stmCache: memory.NewSTMCache(redisClient),
		ctx:      context.Background(),
	},
	nil
}

// Run performs the benchmark
func (b *STMBenchmark) Run(b_testing *testing.B) BenchmarkResult {
	b_testing.ReportAllocs()

	var totalDuration time.Duration
	userID := fmt.Sprintf("benchmark_user_%d", time.Now().UnixNano())

	b_testing.ResetTimer()

	for i := 0; i < b_testing.N; i++ {
		start := time.Now()

		// Simulate a write and a read operation
		event := &memory.STMEvent{
			Type:      memory.STMEventTypeUser,
			Content:   fmt.Sprintf("Benchmark message %d", i),
			Timestamp: time.Now(),
		}

		err := b.stmCache.AddSTMEvent(b.ctx, userID, event)
		if err != nil {
			b_testing.Fatalf("Failed to add event: %v", err)
		}

		_, err = b.stmCache.GetSTMContext(b.ctx, userID)
		if err != nil {
			b_testing.Fatalf("Failed to get context: %v", err)
		}

		totalDuration += time.Since(start)
	}

	b_testing.StopTimer()

	// Calculate results
	totalOps := b_testing.N * 2 // 1 write + 1 read per iteration
	avgLatency := float64(totalDuration.Microseconds()) / float64(totalOps) / 1000.0
	throughput := float64(totalOps) / totalDuration.Seconds()

	return BenchmarkResult{
		TotalOperations: totalOps,
		TotalDuration:   totalDuration,
		AvgLatency:      avgLatency,
		Throughput:      throughput,
	}
}

func main() {
	benchmark, err := NewSTMBenchmark()
	if err != nil {
		fmt.Printf("Error setting up benchmark: %v\n", err)
		os.Exit(1)
	}

	// Run the benchmark
	result := testing.Benchmark(func(b *testing.B) {
		benchmark.Run(b)
	})

	// Convert result to our custom struct for JSON output
	benchmarkResult := BenchmarkResult{
		TotalOperations: int(result.N) * 2,
		TotalDuration:   result.T,
		AvgLatency:      float64(result.T.Microseconds()) / float64(result.N*2) / 1000.0,
		Throughput:      float64(result.N*2) / result.T.Seconds(),
	}

	// Save results to a file
	fileName := fmt.Sprintf("stm_benchmark_results_%s.json", time.Now().Format("20060102_150405"))
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Printf("Failed to create results file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(benchmarkResult); err != nil {
		fmt.Printf("Failed to write results to file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Benchmark results saved to %s\n", fileName)
	fmt.Printf("Total Operations: %d\n", benchmarkResult.TotalOperations)
	fmt.Printf("Total Duration: %s\n", benchmarkResult.TotalDuration)
	fmt.Printf("Average Latency: %.4f ms/op\n", benchmarkResult.AvgLatency)
	fmt.Printf("Throughput: %.2f ops/sec\n", benchmarkResult.Throughput)
}
