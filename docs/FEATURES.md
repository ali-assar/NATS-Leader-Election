# NATS Leader Election - Complete Features Summary

## Overview

This document provides a comprehensive summary of all features implemented in the NATS Leader Election library. The library is production-ready and provides robust, distributed leader election capabilities using NATS JetStream KeyValue.

---

## 🎯 Core Features

### 1. Leader Election

**What it does:** Elects a single leader from multiple competing instances.

**Key Capabilities:**
- ✅ Atomic leader acquisition using NATS KV `Create()` operation
- ✅ Automatic leader election when current leader fails
- ✅ Support for multiple independent elections (multi-role)
- ✅ Fast failover with configurable TTL and heartbeat intervals
- ✅ Sub-second heartbeat support (configurable, default: 1s)

**How it works:**
- Candidates attempt to create a leadership key in NATS KV
- First successful create wins leadership
- Leader maintains ownership via periodic heartbeats
- Followers watch for key deletion/expiration and re-elect

---

### 2. Heartbeat Mechanism

**What it does:** Keeps leadership alive by periodically refreshing the leadership key.

**Key Capabilities:**
- ✅ Configurable heartbeat interval (default: 1s)
- ✅ Revision-based conflict detection
- ✅ Automatic demotion on heartbeat failure
- ✅ Health check integration before each heartbeat
- ✅ Timeout protection for heartbeat operations

**Features:**
- Prevents TTL expiration by updating key before expiry
- Detects conflicts if another leader exists (revision mismatch)
- Handles transient errors with retry logic
- Tracks consecutive failures for demotion threshold

---

### 3. Follower Watch & Re-election

**What it does:** Followers monitor leadership key and trigger re-election when leader fails.

**Key Capabilities:**
- ✅ Real-time key change detection via NATS KV watchers
- ✅ Automatic re-election on key deletion/expiration
- ✅ Leader ID tracking for followers
- ✅ Jitter and backoff to prevent thundering herd
- ✅ Single watcher per election (prevents duplicate watchers)

**Features:**
- Watches for key updates, deletions, and TTL expiration
- Triggers re-election attempts when leadership becomes available
- Updates local state with current leader information
- Prevents race conditions with atomic flags

---

### 4. Fencing Tokens

**What it does:** Prevents split-brain scenarios by validating leadership with unique tokens.

**Key Capabilities:**
- ✅ Unique UUID token per leadership acquisition
- ✅ Periodic token validation (configurable interval)
- ✅ On-demand token validation for critical operations
- ✅ Automatic demotion on token validation failure
- ✅ Token stored in KV value and cached locally

**Features:**
- Each leader gets a unique fencing token (UUID)
- Token validated periodically and before critical operations
- Invalid token indicates another leader exists → automatic demotion
- Prevents stale leaders from performing operations

**Usage:**
```go
// Validate token before critical operation
if !election.ValidateTokenOrDemote(ctx) {
    // Not leader anymore, stop operation
    return
}
// Proceed with critical operation
```

---

### 5. State Management

**What it does:** Tracks election state with thread-safe operations.

**Key Capabilities:**
- ✅ Atomic state transitions (INIT → CANDIDATE → LEADER/FOLLOWER → STOPPED)
- ✅ Thread-safe state access
- ✅ State history tracking (last transition time)
- ✅ Comprehensive status API

**States:**
- `INIT`: Initial state before Start()
- `CANDIDATE`: Attempting to acquire leadership
- `LEADER`: Currently holding leadership
- `FOLLOWER`: Following another leader
- `DEMOTED`: Recently lost leadership
- `STOPPED`: Election stopped

**Status API:**
```go
status := election.Status()
// Returns: State, IsLeader, LeaderID, Token, LastHeartbeat, etc.
```

---

## 🛡️ Robustness Features

### 6. Connection Health Monitoring

**What it does:** Monitors NATS connection status and handles disconnections gracefully.

**Key Capabilities:**
- ✅ Automatic connection status tracking
- ✅ Grace period for brief network hiccups
- ✅ Automatic demotion after grace period expires
- ✅ Leadership verification on reconnection
- ✅ Prevents false positives from temporary disconnects

**Features:**
- Configurable disconnect grace period (default: max(3×HeartbeatInterval, 5s))
- Monitors NATS connection events (disconnect, reconnect, close)
- Verifies leadership still valid after reconnection
- Handles network partitions gracefully

---

### 7. Retry & Backoff Logic

**What it does:** Handles transient failures with intelligent retry strategies.

**Key Capabilities:**
- ✅ Exponential backoff with jitter
- ✅ Configurable max attempts
- ✅ Error classification (transient vs permanent)
- ✅ Circuit breaker pattern support
- ✅ Context-aware cancellation

**Features:**
- Exponential backoff: `initial * multiplier^attempt`
- Random jitter prevents synchronization
- Permanent errors fail immediately (no retry)
- Transient errors retry with backoff
- Circuit breaker prevents retry storms

---

### 8. Error Classification

**What it does:** Distinguishes between retryable and non-retryable errors.

**Key Capabilities:**
- ✅ Custom error types with wrapping support
- ✅ Permanent error detection (invalid config, etc.)
- ✅ Transient error detection (timeouts, network errors)
- ✅ Go 1.13+ error wrapping compatibility

**Error Types:**
- `ErrNotLeader`: Instance is not leader
- `ErrElectionFailed`: Election acquisition failed
- `ErrAlreadyStarted`: Election already started
- `ErrAlreadyStopped`: Election already stopped
- `ErrTokenInvalid`: Fencing token validation failed
- `TimeoutError`: Operation timeout
- `ValidationError`: Configuration validation error

**Classification:**
```go
if IsPermanentError(err) {
    // Don't retry
} else if IsTransientError(err) {
    // Retry with backoff
}
```

---

### 9. Configuration Validation

**What it does:** Validates configuration before starting election to prevent runtime errors.

**Key Capabilities:**
- ✅ Required field validation
- ✅ Value range validation
- ✅ Relationship validation (TTL vs HeartbeatInterval)
- ✅ Clear error messages with field names

**Validation Rules:**
- `Bucket`, `Group`, `InstanceID`: Must be non-empty
- `TTL`, `HeartbeatInterval`: Must be positive
- `TTL >= HeartbeatInterval * 3` (safety margin)
- `ValidationInterval >= HeartbeatInterval` (if set)
- `DisconnectGracePeriod >= HeartbeatInterval * 2` (if set)
- `MaxConsecutiveFailures >= 0` (if set)

---

### 10. Health Checker Integration

**What it does:** Allows leaders to self-monitor and demote if unhealthy.

**Key Capabilities:**
- ✅ Optional health checker interface
- ✅ Health check before each heartbeat
- ✅ Configurable failure threshold
- ✅ Automatic demotion on consecutive failures
- ✅ Fast health checks (100ms timeout)

**Features:**
- Optional: If `HealthChecker` is nil, health checking is disabled
- Health check runs before each heartbeat
- Tracks consecutive failures
- Demotes after `MaxConsecutiveFailures` threshold (default: 3)
- Prevents false positives with failure counting

**Usage:**
```go
type MyHealthChecker struct{}

func (h *MyHealthChecker) Check(ctx context.Context) bool {
    // Check database, external services, etc.
    return isHealthy
}

cfg := ElectionConfig{
    HealthChecker: &MyHealthChecker{},
    MaxConsecutiveFailures: 3,
}
```

---

### 11. Priority-Based Takeover

**What it does:** Allows higher-priority instances to preempt lower-priority leaders.

**Key Capabilities:**
- ✅ Optional priority field in `ElectionConfig`
- ✅ Priority stored in KV payload
- ✅ Atomic priority-based takeover using revision-based `Update()`
- ✅ Opt-in via `AllowPriorityTakeover` flag (disabled by default)
- ✅ Takeover detection in watcher and heartbeat
- ✅ Configuration validation

**Features:**
- Higher number = higher priority
- Priority 0 = no priority (disabled, backward compatible)
- Atomic takeover prevents split-brain (revision-based Update)
- Only attempts takeover if strictly higher priority
- Leader detects takeover on next heartbeat failure
- Follower detects takeover opportunity via watcher

**⚠️ Warning:** Priority takeover can cause service disruption. Use with caution.

**Usage:**
```go
cfg := ElectionConfig{
    // ... other fields ...
    Priority:              20, // Higher number = higher priority
    AllowPriorityTakeover: true, // Must explicitly enable
}
```

**When to use:**
- Primary/backup datacenter scenarios
- Maintenance mode (lower priority to allow takeover)
- Clear priority hierarchy

**When NOT to use:**
- General load balancing
- Automatic failover (use health checks instead)
- Unclear priority requirements

---

### 12. Graceful Shutdown

**What it does:** Provides controlled shutdown with configurable options.

**Key Capabilities:**
- ✅ Context-aware shutdown with timeout
- ✅ Optional key deletion for fast failover
- ✅ Optional callback waiting
- ✅ Goroutine cleanup with timeout
- ✅ Backward compatible `Stop()` method

**Features:**
- `StopWithContext(ctx, opts)` for graceful shutdown
- `DeleteKey`: Delete leadership key immediately (fast failover)
- `WaitForDemote`: Wait for OnDemote callback to complete
- `Timeout`: Maximum time to wait for shutdown
- Waits for all goroutines to finish

**Usage:**
```go
err := election.StopWithContext(ctx, StopOptions{
    DeleteKey:     true,  // Fast failover
    WaitForDemote: true,  // Wait for cleanup
    Timeout:       10 * time.Second,
})
```

---

## 📊 Observability Features

### 13. Structured Logging

**What it does:** Provides comprehensive logging throughout the election lifecycle.

**Key Capabilities:**
- ✅ Structured logging with zap-compatible interface
- ✅ Context-aware logging (instance_id, group, bucket, correlation_id)
- ✅ Error type classification in logs
- ✅ Log levels: Debug, Info, Warn, Error, Fatal
- ✅ Optional: No-op logger when not configured

**Log Points:**
- State transitions (becomeLeader, becomeFollower, Stop)
- Election operations (Start, attemptAcquire, StopWithContext)
- Heartbeat operations (success, failures, health checks)
- Token validation (failures, recoveries)
- Connection management (disconnects, reconnects, grace period)
- Watch events (key deletions, leader changes)
- Error handling (all error paths with classification)

**Usage:**
```go
cfg := ElectionConfig{
    Logger: myZapLogger,  // Optional: nil = no-op
}
```

---

### 14. Prometheus Metrics

**What it does:** Provides comprehensive metrics for monitoring election health and performance.

**Key Capabilities:**
- ✅ Full Prometheus integration
- ✅ 8 metric types (gauges, counters, histograms)
- ✅ Consistent labels (role, instance_id, bucket)
- ✅ Optional: No-op metrics when not configured
- ✅ Automatic metric registration

**Metrics Provided:**

**Gauges:**
- `election_is_leader`: 1 if leader, 0 otherwise
- `election_connection_status`: Connection status (1=connected, 0=disconnected)

**Counters:**
- `election_transitions_total`: State transitions (from_state, to_state)
- `election_failures_total`: Election failures (error_type)
- `election_acquire_attempts_total`: Acquisition attempts (status)
- `election_token_validation_failures_total`: Token validation failures

**Histograms:**
- `election_heartbeat_duration_seconds`: Heartbeat duration (1ms to ~1s buckets)
- `election_leader_duration_seconds`: Leadership duration (1s to ~1h buckets)

**Usage:**
```go
registry := prometheus.NewRegistry()
metrics := leader.NewPrometheusMetrics(registry)

cfg := ElectionConfig{
    Metrics: metrics,  // Optional: nil = no-op
}
```

---

## 🔄 Multi-Role Support

### 15. Multiple Independent Elections

**What it does:** Allows one instance to participate in multiple independent leader elections.

**Key Capabilities:**
- ✅ Multiple elections per instance
- ✅ Different `Group` values = different roles
- ✅ Independent leader/follower states per role
- ✅ Role-specific callbacks
- ✅ Independent failover per role

**How it works:**
- Each role uses a different `Group` name in `ElectionConfig`
- Each election is completely independent
- One instance can be leader for multiple roles
- Different instances can lead different roles simultaneously

**Example:**
```go
// Role 1: Database Writer
dbElection, _ := NewElectionWithConn(nc, ElectionConfig{
    Group: "database-writer",
    // ...
})

// Role 2: Scheduler
schedulerElection, _ := NewElectionWithConn(nc, ElectionConfig{
    Group: "scheduler",
    // ...
})
```

**Use Cases:**
- Workload distribution across instances
- Different responsibilities per instance
- Independent failover for different tasks

---

## 🎣 Callbacks & Events

### 16. OnPromote Callback

**What it does:** Called when instance becomes leader.

**Key Capabilities:**
- ✅ Receives cancellable context (cancelled on demotion/stop)
- ✅ Receives fencing token
- ✅ Runs in separate goroutine
- ✅ Context cancellation for cleanup

**Usage:**
```go
election.OnPromote(func(ctx context.Context, token string) {
    // Start leader-specific tasks
    // Context cancelled when demoted/stopped
})
```

---

### 17. OnDemote Callback

**What it does:** Called when instance loses leadership.

**Key Capabilities:**
- ✅ Synchronous or asynchronous execution (configurable)
- ✅ No parameters (use closure for context)
- ✅ Optional waiting in `StopWithContext`

**Usage:**
```go
election.OnDemote(func() {
    // Stop leader-specific tasks
    // Clean up resources
})
```

---

## 🧪 Testing Features

### 18. Mock Infrastructure

**What it does:** Provides in-memory mocks for isolated unit testing.

**Key Capabilities:**
- ✅ Mock NATS connection
- ✅ Mock KeyValue store
- ✅ Thread-safe operations
- ✅ Revision tracking
- ✅ TTL simulation
- ✅ Watch support with event channels
- ✅ Failure simulation

**Files:**
- `internal/natsmock/connection.go`
- `internal/natsmock/keyvalue.go`

---

### 19. Integration Tests

**What it does:** Tests with real NATS server for production-like scenarios.

**Key Capabilities:**
- ✅ Embedded NATS server for testing
- ✅ Real JetStream KV operations
- ✅ Full election lifecycle testing
- ✅ Multiple instances competing
- ✅ Leader takeover scenarios

**Files:**
- `leader/integration_test.go`
- `leader/real_integration_test.go`
- `leader/embedded_nats_server.go`

---

### 20. Chaos Tests

**What it does:** Tests system behavior under failure conditions.

**Key Capabilities:**
- ✅ Network partition scenarios
- ✅ NATS server restart
- ✅ Process kill scenarios
- ✅ Thundering herd prevention
- ✅ Timing calculation helpers

**Test Scenarios:**
- `TestChaos_NATSServerRestart`: Server restart handling
- `TestChaos_NetworkPartition`: Network partition handling
- `TestChaos_ProcessKill`: Abrupt process termination
- `TestChaos_ProcessKillWithDeleteKey`: Graceful shutdown
- `TestChaos_ThunderingHerd`: Multiple candidates competing

**Files:**
- `leader/chaos_test.go`
- `leader/chaos_test_helpers.go`

---

## 📚 Example Applications

### 21. Basic Demo

**What it does:** Simple example showing basic leader election usage.

**Features:**
- Basic election setup
- OnPromote and OnDemote callbacks
- Status monitoring
- Graceful shutdown
- Connection error handling

**Location:** `cmd/demo/main.go`

---

### 22. Control Manager Example

**What it does:** Realistic example showing task management with leader election.

**Features:**
- Leader-specific task management
- Token validation before critical operations
- Context cancellation for task cleanup
- Resource management with WaitGroup
- Connection error handling

**Location:** `examples/control_manager/main.go`

---

### 23. Multi-Role Example

**What it does:** Demonstrates managing multiple role elections simultaneously.

**Features:**
- Multiple independent role elections
- Role-specific callbacks
- Status monitoring for all roles
- Connection error handling

**Location:** `examples/multi_role/main.go`

---

## 🎨 Design Patterns

### 24. Thread Safety

**What it does:** Ensures safe concurrent access to election state.

**Patterns:**
- Atomic operations for hot paths (`isLeader`, `leaderID`, `token`)
- Mutex protection for complex state updates
- Atomic flags to prevent race conditions
- WaitGroup for goroutine lifecycle management

---

### 25. Context Propagation

**What it does:** Provides cancellation and timeout support throughout the system.

**Patterns:**
- Context-aware operations (Start, Stop, ValidateToken)
- Cancellable OnPromote callback context
- Timeout protection for all operations
- Graceful shutdown with context cancellation

---

### 26. Error Wrapping

**What it does:** Provides structured error handling with Go 1.13+ error wrapping.

**Patterns:**
- Custom error types with `Unwrap()` method
- Error classification (permanent vs transient)
- Error wrapping for context preservation
- `errors.Is()` and `errors.As()` compatibility

---

## 📈 Performance Characteristics

### 27. Fast Failover

**What it does:** Enables quick leader election when current leader fails.

**Capabilities:**
- Sub-second heartbeat intervals (configurable)
- Immediate key deletion on graceful shutdown
- Fast detection via watchers (real-time)
- Configurable TTL for balance between safety and speed

**Typical Timings:**
- Heartbeat interval: 1s (configurable)
- TTL: 3× heartbeat interval (minimum)
- Failover time: TTL + detection delays (~5-10s typical)
- With DeleteKey: ~500ms-1s (immediate deletion)

---

### 28. Thundering Herd Prevention

**What it does:** Prevents all followers from competing simultaneously when leader fails.

**Mechanisms:**
- Random jitter before first attempt (10-100ms)
- Exponential backoff for retries
- Periodic check intervals (500ms)
- Single watcher per election

---

## 🔧 Configuration Options

### 29. Flexible Configuration

**What it does:** Provides extensive configuration options for different use cases.

**Required Fields:**
- `Bucket`: NATS KV bucket name
- `Group`: Election group/role name
- `InstanceID`: Unique instance identifier
- `TTL`: Key time-to-live
- `HeartbeatInterval`: Heartbeat frequency

**Optional Fields:**
- `Logger`: Structured logger (nil = no-op)
- `Metrics`: Prometheus metrics (nil = no-op)
- `ValidationInterval`: Token validation frequency
- `DisconnectGracePeriod`: Grace period for disconnects
- `HealthChecker`: Health check callback (nil = disabled)
- `MaxConsecutiveFailures`: Health check failure threshold

---

## 🚀 Production Readiness

### 30. Production Features

**What it does:** Provides features required for production deployments.

**Features:**
- ✅ Comprehensive error handling
- ✅ Connection resilience
- ✅ Health monitoring
- ✅ Observability (logging + metrics)
- ✅ Graceful shutdown
- ✅ Configuration validation
- ✅ Extensive test coverage
- ✅ Documentation

---

## 📋 Summary

### Implemented Features (✅)

1. **Core Election:** Leader election, heartbeats, watchers, state management
2. **Robustness:** Fencing tokens, connection monitoring, retry/backoff, error classification
3. **Configuration:** Validation, flexible options
4. **Health:** Health checker integration
5. **Shutdown:** Graceful shutdown with options
6. **Observability:** Structured logging, Prometheus metrics
7. **Multi-Role:** Multiple independent elections
8. **Priority:** Priority-based takeover (opt-in)
9. **Testing:** Unit tests, integration tests, chaos tests, mocks
10. **Examples:** Basic demo, control manager, multi-role

### Optional Future Enhancements (📋)

- RoleManager helper (convenience wrapper for multi-role)
- CLI tooling
- gRPC task distribution
- Advanced observability features

---

## 🎯 Use Cases

### Suitable For:
- ✅ Distributed task scheduling
- ✅ Database write coordination
- ✅ Metrics aggregation
- ✅ Control plane coordination
- ✅ Multi-role workload distribution
- ✅ Active/passive high availability
- ✅ Kubernetes, VMs, bare metal, edge devices

### Not Suitable For:
- ❌ Very high-frequency leader changes (< 100ms)
- ❌ Extremely large number of candidates (> 1000)
- ❌ Cross-datacenter scenarios (use NATS clustering)

---

## 📖 Documentation

Comprehensive documentation available:
- `docs/readme.md`: Main README with API reference
- `docs/IMPLEMENTATION_SUMMARY.md`: Detailed implementation guide
- `docs/ROLES_VS_STATES.md`: Multi-role explanation
- `docs/FEATURES.md`: This document
- `examples/README.md`: Example usage guide

---

## 🔗 Related Files

**Core Implementation:**
- `leader/election.go`: Interfaces and configuration
- `leader/kv_election.go`: Core election logic
- `leader/heartbeat.go`: Heartbeat mechanism
- `leader/watcher.go`: Follower watch loop
- `leader/fencing.go`: Token validation
- `leader/connection.go`: Connection monitoring
- `leader/retry.go`: Retry and backoff
- `leader/validation.go`: Configuration validation
- `leader/health.go`: Health checker interface
- `leader/logger.go`: Logging interface
- `leader/metrics.go`: Metrics interface

**Testing:**
- `leader/*_test.go`: Unit tests
- `leader/integration_test.go`: Integration tests
- `leader/chaos_test.go`: Chaos tests
- `internal/natsmock/`: Mock infrastructure

**Examples:**
- `cmd/demo/main.go`: Basic demo
- `examples/control_manager/main.go`: Control manager
- `examples/multi_role/main.go`: Multi-role example

---

*Last Updated: Based on implementation through Step 13 (Observability), Step 14-15 (Integration Tests & Examples), and Priority-Based Takeover*

