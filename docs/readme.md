# NATS Leader Election — Go Library

Lightweight, portable, application-level leader election library built on **NATS KeyValue** (JetStream KV which uses RAFT under the hood). Designed to make applications act active/passive or to assign roles/tasks to specific instances in multi-environment deployments (Kubernetes, VMs, edge devices, etc.).

## Installation

```bash
go get github.com/ali-assar/NATS-Leader-Election@latest
```

See [Quick Start](#quick-start) section below for usage examples.

---

## 🎯 Quick Status

**✅ Production Ready:** Core leader election functionality is fully implemented and tested.

**✅ Implemented:**
- Core election logic, heartbeats, watchers
- Fencing tokens with validation
- Connection health monitoring
- Health checker integration
- Graceful shutdown
- Full observability (metrics + logging)
- Priority-based takeover
- Comprehensive test suite

**📋 TODO:**
- RoleManager for multi-role management
- CLI tooling
- gRPC task distribution

**See "Feature Status" section below for complete details.**

---

## Goal

Provide a minimal, reliable, and production-ready Go library that:

* Elects leaders using NATS KV (no custom RAFT implementation required) ✅
* Supports *application-level* leadership (not restricted to K8s Pods) ✅
* Enables multi-role election (multiple, independent leadership groups) ✅ *Note: Create multiple Election instances for different roles. RoleManager helper is TODO.*
* Exposes simple callbacks to run leader-specific logic and respond to demotions ✅
* Provides robustness primitives (fencing tokens, TTL heartbeats, health-based demotion) ✅
* Is easy to test, observe, and operate ✅

---

## Why this instead of Kubernetes leader election?

Short summary (details later in this README): 

* **Platform independence** — works on Kubernetes, VMs, bare metal, and edge devices
* **Lower latency** — sub-second heartbeats and fast failover (configurable) vs Kubernetes' typical 10s+ lease timing
* **Multi-role, multi-group elections** — elect multiple leaders for different concerns (scheduler, network, metrics) simultaneously
* **Minimal dependency surface** — just a NATS connection + KV bucket; no Kubernetes API clients or RBAC

---

## High-level design

```
+----------------------+      +----------------------+      +----------------------+
| app instance A       |<---->| NATS (JetStream KV)  |<---->| app instance B       |
| - election client    |      | - KV (leaders/)      |      | - election client    |
| - tasks if leader    |      | - RAFT consensus     |      | - tasks if leader    |
+----------------------+      +----------------------+      +----------------------+
```

Key concepts:

* **KV bucket**: A JetStream KeyValue bucket (e.g., `leaders`) holds keys per election group/role (e.g., `control-manager`, `scheduler`).
* **Key value**: Each election key's value contains `{instance_id, token, meta}` and KV entries are created/updated with TTL (MaxAge) for ephemeral leadership.
* **Create semantics**: A candidate tries `kv.Create(key, value)` and wins only if the key didn't exist (atomic create). If it fails, the candidate is a follower.
* **Heartbeat**: The leader periodically refreshes the key using `kv.Update(..., expectedRevision)` or by re-creating with proper revision semantics to keep TTL alive.
* **Watchers**: Followers watch the key's updates/deletes. When the key disappears (TTL expiry or delete) they attempt to acquire leadership.
* **Fencing token**: Each successful acquisition includes a unique token (UUID). Tasks performed by leader require verifying token validity where necessary to prevent stale leaders acting after demotion.

---

## Core API (recommended)

```go
type Election interface {
    // Start begins participating in the election (runs in background goroutines).
    Start(ctx context.Context) error

    // Stop stops election participation and cleans up background goroutines.
    // For graceful shutdown, use StopWithContext instead.
    Stop() error

    // StopWithContext stops election participation with a timeout context.
    // If leader, optionally deletes the key for fast failover.
    // Waits for OnDemote callback to complete before returning.
    StopWithContext(ctx context.Context, opts StopOptions) error

    // IsLeader returns true if this instance currently holds leadership for the configured group/role.
    IsLeader() bool

    // LeaderID returns the current leader instance ID (may be empty if unknown).
    LeaderID() string

    // Token returns the current fencing token for this instance if leader.
    Token() string

    // Status returns detailed status information about the election.
    Status() ElectionStatus

    // OnPromote registers a callback executed when this instance becomes leader.
    OnPromote(func(ctx context.Context, token string))

    // OnDemote registers a callback executed when this instance loses leadership.
    OnDemote(func())

    // ValidateToken validates the current token against the KV store.
    // Returns true if token is valid, false if invalid or not leader.
    ValidateToken(ctx context.Context) (bool, error)

    // ValidateTokenOrDemote validates the token and demotes if invalid.
    // Returns true if token is valid, false if invalid (and demoted).
    ValidateTokenOrDemote(ctx context.Context) bool
}

type ElectionConfig struct {
    // Required fields
    Bucket            string        // NATS KV bucket name
    Group             string        // Election group/role key
    InstanceID        string        // Unique instance identifier
    TTL               time.Duration // Key TTL (must be >= HeartbeatInterval * 3)
    HeartbeatInterval time.Duration // How often to refresh leadership

    // Optional fields
    Logger            Logger        // Structured logger (defaults to no-op)
    
    // Fencing configuration
    ValidationInterval time.Duration // How often to validate token in background (0 = disabled)
    
    // Connection management
    DisconnectGracePeriod  time.Duration // How long to wait before demoting on disconnect (0 = default: max(3*HeartbeatInterval, 5s))
    
    // Observability (OPTIONAL)
    Metrics Metrics // Optional: nil = disabled, non-nil = enabled
    
    // Health checking (OPTIONAL)
    HealthChecker          HealthChecker // Optional: nil = disabled, non-nil = enabled
    MaxConsecutiveFailures int           // Max consecutive failures before demotion (default: 3, only used if HealthChecker is set)
    
    // Priority-based takeover (OPTIONAL)
    Priority               int           // Priority for leader selection (higher number = higher priority, default: 0 = no priority)
    AllowPriorityTakeover  bool          // Enable priority-based preemption (default: false, disabled for safety)
}

// Note: Fencing tokens are automatically validated periodically if ValidationInterval > 0.
// Use ValidateToken() or ValidateTokenOrDemote() methods for operation-level validation.

type StopOptions struct {
    // DeleteKey: delete the leadership key on stop (enables fast failover)
    DeleteKey bool
    
    // WaitForDemote: wait for OnDemote callback to complete
    WaitForDemote bool
    
    // Timeout: maximum time to wait for graceful shutdown
    Timeout time.Duration
}

type ElectionStatus struct {
    State            string        // Current state: INIT, CANDIDATE, LEADER, FOLLOWER, DEMOTED, STOPPED
    IsLeader         bool          // Convenience: true if state == LEADER
    LeaderID         string        // Current leader instance ID
    Token            string        // Current fencing token (if leader)
    LastHeartbeat    time.Time     // Last successful heartbeat (if leader)
    LastTransition   time.Time     // When state last changed
    Revision         uint64        // Current KV revision (if leader)
    ConnectionStatus string        // NATS connection status: CONNECTED, DISCONNECTED, CLOSED
}

type HealthChecker interface {
    // Check returns true if the instance is healthy and should retain leadership
    Check(ctx context.Context) bool
}

// NewElection creates a new election instance with a JetStreamProvider.
func NewElection(nc JetStreamProvider, cfg ElectionConfig) (Election, error)

// NewElectionWithConn creates a new election instance with a NATS connection.
// This is a convenience function that wraps the connection.
func NewElectionWithConn(nc *nats.Conn, cfg ElectionConfig) (Election, error)
```

**Configuration Validation**: The library validates that `TTL >= HeartbeatInterval * 3` (minimum safety margin), `InstanceID` is non-empty, and required fields are set. Invalid configurations return an error.

---

## How it works (step-by-step)

### 1) Boot & Attempt

1. Candidate computes value payload `{id: instanceID, token: uuid(), priority: <priority>}` (priority included if configured).
2. Candidate calls `kv.Create(groupKey, payload, nats.WithMaxAge(ttl))`.

   * If `Create` succeeds → candidate is leader. Callbacks `OnPromote` run.
   * If `Create` fails (key exists) → candidate becomes follower and starts watching the key.

### 2) Leader heartbeat

* Leader refreshes the key to prevent TTL expiry. There are two common strategies:

  1. `kv.Update(key, payload, expectedRevision)` — ensures the revision matches (prevents blind writes)
  2. Re-create with the same key and new revision when API supports it
* If an update fails due to revision mismatch → leader detects a conflict and demotes itself.

### 3) Follower watch + contest

* Followers watch key events (`kv.Watch(groupKey)`)
* **Periodic check**: Every 500ms, followers check if key exists (fallback for missed watch events)
* On `Delete` or missing key (TTL expire) or periodic check detecting deletion, followers race to `Create` again
* Each follower waits random jitter (10-100ms) before attempting to acquire
* One of them wins the next election

### 4) Demotion & fencing

* When leadership is lost, the demoted instance stops leader-only tasks.
* Operations that could be unsafe if performed by stale leaders should validate the fencing token against KV before performing effectful actions. Example: leader holds token `T1`. If it loses leadership and token changes to `T2`, any write must fail if token no longer matches.

---

## Feature Status

### ✅ Implemented Features

#### Core Functionality
- ✅ **Leader Election**: Atomic `Create()` for leadership acquisition
- ✅ **Heartbeat Mechanism**: Periodic `Update()` with revision checking to maintain leadership
- ✅ **Follower Watching**: Watch key events and periodic checks (500ms) for reliable detection
- ✅ **State Management**: Full state machine (INIT → CANDIDATE → LEADER/FOLLOWER → DEMOTED → STOPPED)
- ✅ **Callbacks**: `OnPromote()` and `OnDemote()` callbacks with context support

#### Robustness Features
- ✅ **Fencing Tokens**: UUID-based tokens stored in KV, validated periodically and on-demand
  - ✅ Periodic background validation (configurable `ValidationInterval`)
  - ✅ Operation-level validation via `ValidateToken()` and `ValidateTokenOrDemote()`
  - ✅ Token caching with periodic refresh
- ✅ **Health-Aware Demotion**: Optional `HealthChecker` interface
  - ✅ Health checked before each heartbeat
  - ✅ Automatic demotion after N consecutive failures (configurable)
- ✅ **Connection Health Monitoring**: NATS connection status tracking
  - ✅ Disconnect detection with grace period
  - ✅ Automatic demotion after grace period expires
  - ✅ Reconnection verification before resuming leadership
- ✅ **Retry & Backoff**: Exponential backoff with jitter
  - ✅ Initial jitter (10-100ms) to prevent thundering herd
  - ✅ Exponential backoff with 10% jitter (50ms base, max 5s)
  - ✅ Max 3 retries for acquisition attempts
  - ✅ Circuit breaker pattern for consecutive failures
- ✅ **Error Classification**: Transient vs permanent error handling
  - ✅ Automatic retry for transient errors
  - ✅ Fail-fast for permanent errors
- ✅ **Priority-Based Takeover**: Optional priority system for leader selection
  - ✅ `Priority` field in `ElectionConfig` (higher number = higher priority)
  - ✅ Priority stored in KV payload
  - ✅ Atomic priority-based takeover using revision-based `Update()`
  - ✅ `AllowPriorityTakeover` config flag (opt-in, disabled by default)
  - ✅ Takeover detection in watcher and heartbeat
  - ⚠️ **Note**: Use with caution - prefer voluntary demotion over forced preemption

#### Observability
- ✅ **Structured Logging**: Zap-based logging with correlation IDs
  - ✅ All state transitions logged
  - ✅ Error logging with context
  - ✅ Configurable log levels
- ✅ **Prometheus Metrics**: Full metrics integration
  - ✅ `election_is_leader` (gauge)
  - ✅ `election_transitions_total` (counter)
  - ✅ `election_failures_total` (counter)
  - ✅ `election_heartbeat_duration_seconds` (histogram)
  - ✅ `election_leader_duration_seconds` (histogram)
  - ✅ `election_acquire_attempts_total` (counter)
  - ✅ `election_token_validation_failures_total` (counter)
  - ✅ `election_connection_status` (gauge)

#### Graceful Shutdown
- ✅ **StopWithContext()**: Graceful shutdown with options
  - ✅ Optional key deletion for fast failover
  - ✅ Wait for `OnDemote` callback completion
  - ✅ Timeout handling
  - ✅ Context propagation to callbacks

#### Testing
- ✅ **Unit Tests**: Comprehensive unit tests with mocks
- ✅ **Integration Tests**: Real NATS server integration tests
- ✅ **Chaos Tests**: Network partition, process kill, NATS restart, thundering herd
- ✅ **Test Infrastructure**: Embedded NATS server helpers

### 📋 TODO / Not Yet Implemented

#### Multi-Role Support
- 📋 **RoleManager**: Convenience helper to manage multiple elections
  - TODO: Create `RoleManager` interface and implementation
  - TODO: Aggregate status across multiple roles
  - TODO: Role-specific callbacks

#### Task Distribution
- 📋 **gRPC Control Plane**: Task distribution from leader to followers
  - TODO: Create `control` package for task distribution
  - TODO: Leader authentication using fencing tokens
  - TODO: Task subscription mechanism for followers

#### Tooling & Examples
- 📋 **CLI Tool**: Debugging and management tool
  - TODO: List current leaders
  - TODO: Force demote leaders
  - TODO: Inspect tokens and election state
- 📋 **Example Applications**: 
  - TODO: Update `cmd/demo/main.go` with complete example
  - TODO: Create `examples/control-manager/` example
  - TODO: Create `examples/multi-role/` example
  - TODO: Kubernetes operator example

#### Advanced Observability
- 📋 **Advanced Metrics**: Additional metrics and exporters
  - TODO: Official Prometheus exporter package
  - TODO: Additional performance metrics
  - TODO: Distributed tracing integration

#### Multi-Datacenter
- 📋 **Multi-Datacenter Support**: Cross-cluster leader election
  - TODO: Support for multiple NATS clusters
  - TODO: Datacenter-aware leader selection
  - TODO: Integration with service discovery (Consul/etcd)

#### Security Enhancements
- 📋 **Token Signing**: Cryptographically signed tokens
  - TODO: Implement token signing/verification
  - TODO: Add signing key management
- 📋 **Token Rotation**: Periodic token rotation for long-lived leaders
  - TODO: Implement token rotation mechanism
  - TODO: Configurable rotation interval

#### Performance Optimizations
- 📋 **Connection Pooling**: Optimize for multiple elections
  - TODO: Connection pool management
  - TODO: Shared connection optimization
- 📋 **Batch Operations**: Batch KV operations where possible
  - TODO: Batch heartbeat updates
  - TODO: Batch status checks

#### Additional Features
- 📋 **Observer Mode**: Watch elections without participating
  - TODO: Read-only election observer interface
  - TODO: Observer callbacks for leader changes
- 📋 **Bucket Auto-Create**: Automatic bucket creation
  - TODO: Implement `BucketAutoCreate` option
  - TODO: Bucket configuration management

---

## Quick Start

### Prerequisites

1. **NATS Server** with JetStream enabled
   ```bash
   # Using Docker
   docker run -d -p 4222:4222 -p 8222:8222 nats:latest -js
   
   # Or install locally
   # https://docs.nats.io/running-a-nats-service/introduction/installation
   ```

2. **Go 1.19+**

3. **Install the library**
   ```bash
   go get github.com/ali-assar/NATS-Leader-Election/leader
   ```

### Basic Example

```go
package main

import (
    "context"
    "errors"
    "log"
    "time"
    
    "github.com/nats-io/nats.go"
    "github.com/ali-assar/NATS-Leader-Election/leader"
)

func main() {
    // Connect to NATS
    nc, err := nats.Connect(nats.DefaultURL)
    if err != nil {
        log.Fatal(err)
    }
    defer nc.Close()

    // Create JetStream context and KV bucket
    js, err := nc.JetStream()
    if err != nil {
        log.Fatal(err)
    }

    // Create KV bucket (only needed once, or use existing bucket)
    _, err = js.CreateKeyValue(&nats.KeyValueConfig{
        Bucket: "leaders",
        TTL:    10 * time.Second,
    })
    if err != nil && !errors.Is(err, nats.ErrBucketExists) {
        log.Fatal(err)
    }

    // Configure election
    cfg := leader.ElectionConfig{
        Bucket:            "leaders",
        Group:             "my-app",
        InstanceID:        "instance-1", // Unique per instance
        TTL:               10 * time.Second,
        HeartbeatInterval: 1 * time.Second,
    }

    // Create election instance
    election, err := leader.NewElectionWithConn(nc, cfg)
    if err != nil {
        log.Fatal(err)
    }

    // Register callbacks
    election.OnPromote(func(ctx context.Context, token string) {
        log.Printf("✅ Became leader! Token: %s", token)
        // Start leader tasks here
    })

    election.OnDemote(func() {
        log.Println("❌ Lost leadership")
        // Stop leader tasks here
    })

    // Start election
    ctx := context.Background()
    if err := election.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer election.Stop()

    // Run until interrupted
    log.Println("Election started. Press Ctrl+C to stop.")
    <-ctx.Done()
}
```

### Running Multiple Instances

Run the same program on multiple machines/containers with different `InstanceID` values:

```bash
# Instance 1
INSTANCE_ID=instance-1 go run main.go

# Instance 2
INSTANCE_ID=instance-2 go run main.go

# Instance 3
INSTANCE_ID=instance-3 go run main.go
```

Only one instance will be leader at a time. If the leader stops, another instance will automatically take over.

---

## Usage Examples

```go
import (
    "context"
    "log"
    "time"
    
    "github.com/nats-io/nats.go"
    "github.com/ali-assar/NATS-Leader-Election/leader"
)

// Connect to NATS
nc, err := nats.Connect(nats.DefaultURL)
if err != nil {
    log.Fatal(err)
}
defer nc.Close()

// Create JetStream context and KV bucket
js, err := nc.JetStream()
if err != nil {
    log.Fatal(err)
}

kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
    Bucket: "leaders",
    TTL:    10 * time.Second,
})
if err != nil {
    log.Fatal(err)
}

// Configure election
cfg := leader.ElectionConfig{
    Bucket:            "leaders",
    Group:             "control-manager",
    InstanceID:        "instance-1", // Should be unique per instance
    TTL:               10 * time.Second,
    HeartbeatInterval: 1 * time.Second,
    ValidationInterval: 5 * time.Second, // Validate token every 5s
    // Logger: myLogger,              // Optional: structured logger
    // Metrics: myMetrics,            // Optional: Prometheus metrics
    // HealthChecker: myHealthChecker, // Optional: health checker
    // Priority: 10,                  // Optional: priority for takeover (higher = higher priority)
    // AllowPriorityTakeover: true,   // Optional: enable priority-based preemption (default: false)
}

e, err := leader.NewElectionWithConn(nc, cfg)
if err != nil {
    log.Fatal(err)
}

// Register callbacks
e.OnPromote(func(ctx context.Context, token string) {
    log.Println("Promoted — starting leader tasks", "token", token)
    go startLeaderTasks(ctx, token)
})

e.OnDemote(func() {
    log.Println("Demoted — stopping leader tasks")
    stopLeaderTasks()
})

// Start election
ctx := context.Background()
if err := e.Start(ctx); err != nil {
    log.Fatal(err)
}
defer e.Stop()

// In leader tasks, validate token before critical operations
func performCriticalOperation(e leader.Election) {
    if !e.ValidateTokenOrDemote(context.Background()) {
        log.Println("Token invalid, operation aborted")
        return
    }
    // Perform critical operation...
}

// Run until shutdown
<-ctx.Done()

// Graceful shutdown
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

err = e.StopWithContext(shutdownCtx, leader.StopOptions{
    DeleteKey:      true,  // Delete key for fast failover
    WaitForDemote:  true,  // Wait for OnDemote to complete
})
if err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

---

## Edge cases & pitfalls

* **Clock skew**: Don't rely on system clocks for ordering; use KV revision and tokens.
* **Network partitions**: Because NATS KV is RAFT-based, it tolerates partitions; but be cautious — if your client loses connectivity but remains running, ensure it demotes on lost heartbeats. The library monitors connection status and demotes if disconnected beyond `DisconnectGracePeriod`.
* **Split-brain**: Using `Create` + `Update` with expected revisions and fencing tokens greatly reduces this risk. Do not implement a takeover by `Put` without revision checks.
* **Thundering herd**: When a leader key expires, many followers may attempt `Create` at once. The library implements randomized jitter (10-100ms initial, exponential backoff with 10% jitter for retries) to reduce load.
* **Unreliable KV TTL semantics**: Test TTL and heartbeat timing under load. Make heartbeat interval significantly smaller than TTL (minimum 3x ratio enforced) and consider performing a `kv.Update` before TTL/2.
* **NATS KV bucket deletion**: If the bucket is deleted while election is running, the library will detect this and return errors. Consider enabling `BucketAutoCreate` for development, but prefer explicit bucket management in production.
* **Error classification**: The library distinguishes between transient errors (network issues, temporary unavailability) and permanent errors (invalid config, permission denied). Transient errors trigger retries; permanent errors fail fast.
* **Connection health**: Monitor NATS connection status. If connection is lost, the library will demote after `DisconnectGracePeriod` to prevent stale leadership.

---

## Testing

**Unit Tests:**
- ✅ Mock `nats.Conn` and `nats.KeyValue` interfaces (`internal/natsmock`)
- ✅ Test helpers that simulate KV state transitions (Create success/fail, Update success/fail, Watch events)
- ✅ Test error classification (transient vs. permanent)
- ✅ Test configuration validation
- ✅ Test retry and backoff logic
- ✅ Test metrics recording
- ✅ Test fencing token validation
- ✅ Test connection health monitoring
- ✅ Test graceful shutdown

**Integration Tests:**
- ✅ Run embedded NATS server (JetStream enabled) with KV buckets
- ✅ Exercise leader takeover, demotion, and fencing
- ✅ Test with multiple candidates competing simultaneously
- ✅ Test connection loss and recovery scenarios
- ✅ Test full election lifecycle with real NATS
- ✅ Test token validation scenarios
- ✅ Test heartbeat maintenance

**Chaos Tests:**
- ✅ Simulate network partitions and verify safe failover
- ✅ Simulate process kills (with and without key deletion)
- ✅ Simulate NATS server restarts
- ✅ Test thundering herd scenarios (10+ followers competing)
- ✅ Test graceful shutdown scenarios

**Test Infrastructure:**
- ✅ Embedded NATS server helper (`embeded_nats_server.go`)
- ✅ KV bucket creation/cleanup helpers
- ✅ Test utilities (`waitForLeader`, `waitForCondition`, `waitForHeartbeat`)

**Property-Based Tests:**
- Use libraries like `gopter` to test state transitions
- Verify invariants (e.g., only one leader at a time)
- Test edge cases in state machine transitions

**Load Tests:**
- Many candidates competing simultaneously
- High-frequency leader transitions
- Long-running elections with many heartbeats

---

## Comparison matrix — NATS KV election vs Kubernetes Lease

| Feature                |          NATS KV (this lib) | Kubernetes Lease                      |
| ---------------------- | --------------------------: | ------------------------------------- |
| Platform portability   |            ✅ works anywhere | ❌ requires K8s API server             |
| Failover latency       | ✅ sub-second (configurable) | ❌ often 10s+ by default               |
| Multi-role support     |      ✅ easy (multiple keys) | ❌ single Lease per use-case           |
| Fencing tokens         |                 ✅ supported | ❌ not standard                        |
| Operational complexity |  ✅ single dependency (NATS) | ❌ kube-client + RBAC + cluster access |

---

## Implementation Status Summary

**Core Features: ✅ Complete**
- All essential leader election functionality is implemented and tested
- Production-ready with comprehensive error handling and observability
- Full test coverage (unit, integration, chaos tests)

**Advanced Features:**
- ✅ Priority-based takeover
- 📋 Multi-role management (RoleManager)
- 📋 gRPC task distribution
- 📋 CLI tooling

**See "Feature Status" section above for detailed breakdown.**

---

## Security considerations

* **TLS**: Use TLS between clients and NATS server to encrypt communication
* **NATS Accounts**: Use NATS accounts/credentials to restrict who can write to KV buckets
* **Fencing Tokens**: Treat fencing tokens as short-lived secrets; avoid logging them in production logs
* **Signed Tokens**: Consider providing signed tokens for stronger guarantees (optional future enhancement)
* **Token Rotation**: Tokens are rotated on each election, but consider periodic rotation for long-lived leaders (optional)
* **Access Control**: Ensure NATS KV bucket permissions are properly configured to prevent unauthorized access

---

## Contributing

* Open an issue for feature requests or bugs
* Add unit tests for new behaviors
* Keep API changes backward compatible; bump major version for breaking changes

---

## License

Choose a permissive license (e.g., MIT or Apache 2.0) and include `LICENSE` in the repo.

---

## Appendix — Example KV payload (JSON)

```json
{
  "id": "instance-1234",
  "token": "3f9a6f8d-1a2b-4cde-9f1a-c0b123456789",
  "priority": 10,
  "meta": {"hostname": "node-01", "region": "eu-west-1"}
}
```

---

## Troubleshooting

### Common Issues

**Issue: Frequent leader transitions**
- **Cause**: TTL too short, heartbeat interval too long, or network instability
- **Solution**: Increase TTL, decrease heartbeat interval, ensure `TTL >= HeartbeatInterval * 3`, check network connectivity

**Issue: Stale leader continues acting**
- **Cause**: Leader lost connectivity but didn't detect it, or token validation is disabled
- **Solution**:
  - Enable token validation: set `ValidationInterval` (e.g., 5 seconds)
  - Use `ValidateTokenOrDemote()` before critical operations
  - Enable connection monitoring (automatic with NATS connection)
  - Check connection status in metrics/logs

**Issue: Election never starts / "bucket not found"**
- **Cause**: KV bucket doesn't exist
- **Solution**:
  - Create the bucket before starting election:
    ```go
    js.CreateKeyValue(&nats.KeyValueConfig{
        Bucket: "leaders",
        TTL:    10 * time.Second,
    })
    ```
  - Or use an existing bucket name

**Issue: "invalid config" error**
- **Cause**: Configuration validation failed
- **Solution**:
  - Ensure `TTL >= HeartbeatInterval * 3`
  - Ensure `InstanceID` is not empty
  - Ensure `Bucket` and `Group` are not empty
  - If using priority takeover, ensure `Priority > 0` when `AllowPriorityTakeover` is true

**Issue: Leader doesn't demote on health check failure**
- **Cause**: Health checker not configured or failure threshold not reached
- **Solution**:
  - Implement and set `HealthChecker` in config
  - Ensure `MaxConsecutiveFailures` is set appropriately (default: 3)
  - Check health checker logs to verify it's being called

**Issue: High CPU usage**
- **Cause**: Heartbeat interval too short or too many elections
- **Solution**:
  - Increase heartbeat interval (e.g., 500ms → 1s)
  - Reduce number of concurrent elections
  - Check metrics for operation frequency

**Issue: Slow failover**
- **Cause**: TTL too long or grace period too long
- **Solution**:
  - Decrease TTL (e.g., 30s → 10s)
  - Decrease `DisconnectGracePeriod`
  - Use `DeleteKey: true` in `StopWithContext` for graceful shutdowns

**Issue: Connection errors / "connection lost"**
- **Cause**: NATS server unreachable or network issues
- **Solution**:
  - Check NATS server is running and accessible
  - Verify network connectivity
  - Check NATS connection status
  - Review connection monitoring logs
  - Consider using NATS connection pooling for multiple elections

### Debugging Tips

1. **Enable structured logging**:
   ```go
   logger, _ := zap.NewDevelopment()
   cfg.Logger = logger
   ```

2. **Enable Prometheus metrics**:
   ```go
   metrics := leader.NewPrometheusMetrics(prometheus.DefaultRegisterer)
   cfg.Metrics = metrics
   ```

3. **Check election status**:
   ```go
   status := election.Status()
   log.Printf("State: %s, IsLeader: %v, LeaderID: %s",
       status.State, status.IsLeader, status.LeaderID)
   ```

4. **Monitor NATS KV directly**:
   ```bash
   # Using NATS CLI
   nats kv get leaders my-app
   nats kv watch leaders my-app
   ```

5. **Check for error patterns**:
   - Review logs for error patterns
   - Monitor metrics for failure rates
   - Check connection status metrics

### Getting Help

- Check the [examples](examples/) directory for complete working examples
- Review the [API documentation](https://pkg.go.dev/github.com/ali-assar/NATS-Leader-Election/leader)
- Open an issue on GitHub with:
  - Configuration details (without secrets)
  - Error messages and logs
  - NATS server version
  - Go version
- **Cause**: Fencing token not being validated before operations
- **Solution**: Use `ValidateToken()` or `ValidateTokenOrDemote()` before critical operations, or enable periodic validation with `ValidationInterval > 0`

**Issue: Election never succeeds**
- **Cause**: Bucket doesn't exist, permissions issue, or NATS connection problems
- **Solution**: Check bucket exists, verify NATS credentials, check connection status via `Status()` method

**Issue: High CPU usage**
- **Cause**: Too frequent heartbeats or watch operations
- **Solution**: Adjust `HeartbeatInterval` and `TTL` to reasonable values (e.g., 1s heartbeat, 5s TTL)

**Issue: Slow failover**
- **Cause**: TTL too long or not deleting key on graceful shutdown
- **Solution**: Reduce TTL (but maintain safety margin), enable `DeleteOnStop` in graceful shutdown

### Performance Tuning

**For low-latency failover:**
- Set `TTL` to 3-5 seconds
- Set `HeartbeatInterval` to 1 second
- Enable `DeleteOnStop` for graceful shutdowns

**For stability over speed:**
- Set `TTL` to 10-30 seconds
- Set `HeartbeatInterval` to 2-5 seconds
- Use longer `DisconnectGracePeriod` to tolerate brief network hiccups

**For high-throughput scenarios:**
- Use connection pooling if managing multiple elections
- Set `ValidationInterval` to reduce validation frequency (e.g., 10s instead of 5s)
- Use `ValidateToken()` only for critical operations, not every operation

---

## Additional Resources

* [NATS JetStream Documentation](https://docs.nats.io/nats-concepts/jetstream)
* [Raft Consensus Algorithm](https://raft.github.io/)
* [Distributed Systems Patterns](https://martinfowler.com/articles/patterns-of-distributed-systems/)
