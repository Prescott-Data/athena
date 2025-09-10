# STM Benchmark Suite

Comprehensive benchmarking tools for comparing Memory OS STM performance with docintel-api.

## Overview

This benchmark suite tests all critical STM (Short-Term Memory) operations to ensure Memory OS maintains feature parity and performance equivalence with the original docintel-api implementation.

## Features Tested

### 🚀 **Performance Metrics**
- **Latency**: Average, P95, P99 response times
- **Throughput**: Operations per second
- **Memory Usage**: Peak and average memory consumption
- **Error Rates**: Failure percentages by operation type

### 🧠 **STM Operations**
- **Cache Operations**: Store, retrieve, update conversation turns
- **Dialogue Chain Decisions**: Cosine similarity + LLM fallback logic
- **Embedding Operations**: Creation, storage, retrieval, similarity search
- **Quality Validation**: All validation modes and scoring algorithms
- **Guardrails Testing**: Rate limiting and circuit breaker behavior

### 📊 **Output Metrics**
- **Cache Hit Rates**: Redis caching effectiveness
- **Quality Score Distribution**: Validation algorithm effectiveness
- **Guardrail Triggers**: Rate limiting and circuit breaker activation
- **Resource Utilization**: CPU and memory consumption patterns

## Usage

### Quick Run
```bash
cd /home/dev/projects/dromos-core/memory-os/scripts/benchmark
go run stm_benchmark.go
```

### Custom Configuration
```bash
# Environment variables
export BENCHMARK_USERS=20
export BENCHMARK_WORKERS=8
export REDIS_HOST=your-redis-host
export MONGO_URI=your-mongo-uri

go run stm_benchmark.go
```

### Configuration Options
- `BENCHMARK_USERS`: Number of test users (default: 10)
- `BENCHMARK_WORKERS`: Concurrent workers (default: 4)
- `BENCHMARK_CONVERSATIONS`: Conversations per user (default: 5)
- `BENCHMARK_TURNS`: Turns per conversation (default: 3)

## Results

### Output Format
1. **Console Output**: Real-time formatted results
2. **JSON File**: Detailed metrics saved as `stm_benchmark_results_YYYYMMDD_HHMMSS.json`

### Sample Output
```
================================================================================
STM BENCHMARK RESULTS - Memory-OS STM
================================================================================
Test Duration: 2m34s
Peak Memory Usage: 45.23 MB
Average Memory Usage: 32.18 MB
Cache Hit Rate: 94.56%

Operation Performance:
Operation                 Avg Latency     Throughput/s    Error %   
--------------------------------------------------------------------------------
cache_store              1.2ms           823.45          0.12      
cache_retrieve           0.8ms           1245.67         0.05      
dialogue_chain_decision  15.3ms          65.43           1.23      
embedding_create         45.2ms          22.15           0.89      
quality_validation       8.7ms           114.82          0.00      

Guardrails:
Rate Limit Triggers: 12
Circuit Breaker Triggers: 3

Quality Score Distribution:
  0.0-0.1: 5 segments
  0.4-0.5: 23 segments
  0.6-0.7: 45 segments
  0.8-0.9: 27 segments
================================================================================
```

## Comparison with docintel-api

To compare with docintel-api, run the same benchmark against both systems:

1. **Memory OS**: Run this benchmark
2. **docintel-api**: Create equivalent benchmark for original system
3. **Compare**: Analyze JSON outputs for performance differences

### Key Comparison Metrics
- **Latency Equivalence**: ±5% acceptable variance
- **Throughput Parity**: Similar ops/second rates
- **Cache Effectiveness**: Hit rates should match
- **Quality Scoring**: Same score distributions
- **Resource Usage**: Memory consumption patterns

## Troubleshooting

### Common Issues
1. **Connection Errors**: Verify Redis/MongoDB connectivity
2. **Memory Issues**: Reduce concurrent workers or test data size
3. **Timeout Errors**: Increase timeouts for slower environments

### Debug Mode
```bash
export LOG_LEVEL=DEBUG
go run stm_benchmark.go
```

## Integration with CI/CD

This benchmark can be integrated into automated testing pipelines:

```yaml
# Example GitHub Actions step
- name: Run STM Benchmark
  run: |
    cd scripts/benchmark
    go run stm_benchmark.go
    # Compare results with baseline
    python compare_results.py baseline.json stm_benchmark_results_*.json
```
