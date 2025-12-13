# Performance Benchmarks

This document provides performance benchmarks for the NATS Leader Election library. These benchmarks help identify performance bottlenecks and measure the overhead of various operations.

## Running Benchmarks

```bash
# Run all benchmarks
go test -bench=. -benchmem ./leader

# Run specific benchmark
go test -bench=BenchmarkIsLeader -benchmem ./leader

# Run with more iterations for better accuracy
go test -bench=. -benchmem -benchtime=10s ./leader
```

## Benchmark Results

Results from Intel Core i3-12100, Go 1.25.4, Linux:

### Core Operations

#### `BenchmarkIsLeader`
Measures the overhead of checking leadership status.
- **Result**: 0.61 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Extremely fast atomic boolean read - suitable for frequent checks
- **Use case**: Frequently called operation, should be extremely fast

#### `BenchmarkStatus`
Measures the overhead of getting full election status.
- **Result**: 20.80 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Fast status retrieval using atomic operations
- **Use case**: Status monitoring, debugging

#### `BenchmarkAcquireLeadership`
Measures the time to acquire leadership (full election cycle).
- **Result**: 990 ms/op, 10.3 KB/op, 83 allocs/op
- **Analysis**: Includes full setup/teardown, goroutine creation, and election cycle
- **Use case**: Initial leader election, failover scenarios

### KV Operations

#### `BenchmarkHeartbeatUpdate`
Measures the time to update the leadership key (heartbeat).
- **Result**: 33.73 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Very fast mock KV update - real NATS will be slower (1-5ms)
- **Use case**: Periodic heartbeat updates (typically every 1-2 seconds)

#### `BenchmarkTokenValidation`
Measures the time to validate a fencing token.
- **Result**: 1.72 μs/op, 880 B/op, 19 allocs/op
- **Analysis**: Fast validation with minimal allocations
- **Use case**: Token validation before critical operations

### Serialization

#### `BenchmarkPayloadMarshal`
Measures JSON marshaling overhead for leadership payload.
- **Result**: 147.9 ns/op, 64 B/op, 1 allocs/op
- **Analysis**: Efficient JSON marshaling with single allocation
- **Use case**: Every heartbeat and acquisition attempt

#### `BenchmarkPayloadUnmarshal`
Measures JSON unmarshaling overhead for leadership payload.
- **Result**: 712.7 ns/op, 248 B/op, 6 allocs/op
- **Analysis**: Reasonable unmarshaling overhead
- **Use case**: Reading leadership key from KV store

### Concurrent Operations

#### `BenchmarkConcurrentAcquisition`
Measures performance under concurrent acquisition attempts.
- **Result**: 1.29 ms/op, 7.3 KB/op, 78 allocs/op
- **Analysis**: Good performance under contention
- **Use case**: Multiple instances competing for leadership

### Utility Operations

#### `BenchmarkLogWithContext`
Measures overhead of creating log context fields.
- **Result**: 40.83 ns/op, 192 B/op, 1 allocs/op
- **Analysis**: Low overhead for structured logging
- **Use case**: Every log statement

#### `BenchmarkMetricsRecording`
Measures overhead of recording Prometheus metrics.
- **Result**: 2.96 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Negligible overhead with no-op metrics
- **Use case**: Every state transition, heartbeat, etc.

#### `BenchmarkErrorClassification`
Measures overhead of error classification.
- **Result**: 371.9 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Fast error classification without allocations
- **Use case**: Error handling and logging

#### `BenchmarkStateTransition`
Measures overhead of atomic state transitions.
- **Result**: 20.12 ns/op, 0 B/op, 0 allocs/op
- **Analysis**: Very fast atomic operations
- **Use case**: State machine transitions

## Performance Characteristics

### Fast Operations (< 1μs)
- `IsLeader()` - Atomic boolean read
- `Status()` - Atomic value reads
- State transitions - Atomic operations
- Metrics recording (no-op) - Function call overhead only

### Medium Operations (1μs - 1ms)
- Token validation - KV Get operation
- Payload marshaling/unmarshaling - JSON operations
- Log context creation - Slice allocation

### Slow Operations (> 1ms)
- Leadership acquisition - Full election cycle with goroutines
- Heartbeat updates - KV Update operation
- Concurrent acquisition - Contention handling

## Optimization Opportunities

1. **Token Validation**: Consider caching token validation results for a short period
2. **Payload Serialization**: Consider using a faster JSON library (e.g., `jsoniter`) if needed
3. **Metrics**: Use no-op metrics in production if metrics are disabled
4. **Logging**: Use structured logging efficiently, avoid creating unnecessary fields

## Real-World Performance

In production with real NATS:
- **Heartbeat latency**: Typically 1-5ms (network + NATS processing)
- **Acquisition latency**: Typically 10-50ms (network + KV operations)
- **Failover time**: TTL + detection delay (typically 1-3 seconds)

## Notes

- Benchmarks use mock KV store, so network latency is not included
- Real-world performance will be slower due to network and NATS processing
- Benchmarks focus on library overhead, not NATS server performance
- Results may vary based on hardware and Go version

