# NATS Leader Election — Go Library

Lightweight, portable, application-level leader election library built on **NATS KeyValue** (JetStream KV which uses RAFT under the hood). Designed to make applications act active/passive or to assign roles/tasks to specific instances in multi-environment deployments (Kubernetes, VMs, edge devices, etc.).

---

## Goal

Provide a minimal, reliable, and production-ready Go library that:

* Elects leaders using NATS KV (no custom RAFT implementation required)
* Supports *application-level* leadership (not restricted to K8s Pods)
* Enables multi-role election (multiple, independent leadership groups)
* Exposes simple callbacks to run leader-specific logic and respond to demotions
* Provides robustness primitives (fencing tokens, TTL heartbeats, health-based demotion)
* Is easy to test, observe, and operate

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
}

type ElectionConfig struct {
    // Required fields
    Bucket            string        // NATS KV bucket name
    Group             string        // Election group/role key
    InstanceID        string        // Unique instance identifier
    TTL               time.Duration // Key TTL (must be >= HeartbeatInterval * 3)
    HeartbeatInterval time.Duration // How often to refresh leadership

    // Optional fields
    Priority          int           // Priority for takeover (higher = preferred)
    Logger            Logger        // Structured logger (defaults to no-op)
    
    // Connection management
    ConnectionTimeout      time.Duration // Timeout for KV operations
    DisconnectGracePeriod  time.Duration // How long to wait before demoting on disconnect
    
    // Fencing configuration
    Fencing FencingConfig // Token validation strategy
    
    // Health checking
    HealthChecker HealthChecker // Optional health check callback
    
    // Retry and backoff
    RetryConfig RetryConfig // Retry strategy for transient failures
    
    // Bucket management
    BucketAutoCreate bool // Create bucket if it doesn't exist
    
    // Graceful shutdown
    DeleteOnStop bool // Delete key on Stop() for fast failover
}

type FencingConfig struct {
    // ValidationInterval: how often to validate token in background (0 = disabled)
    ValidationInterval time.Duration
    
    // ValidateOnCriticalOps: always validate token before critical operations
    ValidateOnCriticalOps bool
    
    // CacheToken: cache token locally and validate periodically (default: true)
    CacheToken bool
}

type RetryConfig struct {
    // MaxAttempts: maximum retry attempts (0 = unlimited)
    MaxAttempts int
    
    // InitialBackoff: initial backoff duration
    InitialBackoff time.Duration
    
    // MaxBackoff: maximum backoff duration
    MaxBackoff time.Duration
    
    // BackoffMultiplier: exponential backoff multiplier
    BackoffMultiplier float64
    
    // Jitter: random jitter range (0-1, e.g., 0.1 = ±10%)
    Jitter float64
}

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

func NewElection(nc *nats.Conn, cfg ElectionConfig) (Election, error)
```

**Configuration Validation**: The library validates that `TTL >= HeartbeatInterval * 3` (minimum safety margin), `InstanceID` is non-empty, and required fields are set. Invalid configurations return an error.

---

## How it works (step-by-step)

### 1) Boot & Attempt

1. Candidate computes value payload `{id: instanceID, token: uuid(), meta: {priority}}`.
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
* On `Delete` or missing key (TTL expire) followers race to `Create` again
* One of them wins the next election

### 4) Demotion & fencing

* When leadership is lost, the demoted instance stops leader-only tasks.
* Operations that could be unsafe if performed by stale leaders should validate the fencing token against KV before performing effectful actions. Example: leader holds token `T1`. If it loses leadership and token changes to `T2`, any write must fail if token no longer matches.

---

## Important improvements & why they matter

Below, each improvement is explained with *what*, *why*, and *how to implement*.

### 1) Fencing tokens (critical)

**What:** a unique token (UUID) stored with the leader key.

**Why:** prevents a previously-leader instance from continuing to act after it loses leadership (due to network partition or delayed demotion). If leader tasks touch shared resources, they should check token validity before important operations.

**How:** store token in KV value. When performing sensitive operations, verify the token by reading KV and comparing. Optionally, use atomic conditional updates that require the token/revision to match.

**Enhanced Strategy:**
- **Periodic validation**: Background validation every N seconds (configurable) to catch stale leadership faster
- **Operation-level validation**: Critical operations validate immediately; non-critical can use cached token
- **Token caching**: Cache token locally with periodic refresh to reduce KV read load while maintaining safety

### 2) Health-aware demotion

**What:** automatic self-demotion if local health checks fail.

**Why:** if the leader is unhealthy (e.g., can't access DB or is overloaded), it should abdicate so a healthier instance can take control.

**How:** integrate a `HealthChecker` callback that returns `healthy bool`. If it returns false for N consecutive checks, call `kv.Delete(key)` or stop heartbeating and voluntarily demote.

### 3) Multi-role & multi-group support

**What:** allow multiple independent election keys (e.g., `roles/scheduler`, `roles/network`) so different instances can hold different roles simultaneously.

**Why:** many control-plane systems need different exclusive actors for different responsibilities.

**How:** provide convenience helpers to create multiple `Election` objects or a `RoleManager` that manages a map of roles → elections.

### 4) Priority-based takeover (optional, use with caution)

**What:** candidates can have a `priority` value; higher priority can pre-empt an existing lower-priority leader when certain safe conditions are met.

**Why:** useful when you want to prefer specific nodes (more powerful machines) to be leaders.

**How:** store `priority` in the KV value. On watcher events, if a candidate sees a lower priority leader AND the leader's token/revision shows inactivity or a manual override flag, implement a controlled takeover.

**Safety Recommendations:**
- **Prefer voluntary demotion**: Higher priority should wait for current leader to become unhealthy rather than forcing preemption
- **Require explicit flag**: Add `AllowPriorityTakeover bool` to config to opt-in
- **Grace period**: Higher priority waits for current leader to be unhealthy (missed heartbeats) before takeover
- **Document risks**: Clearly document split-brain risks and recommend manual demotion over automatic takeover

### 5) Observability & metrics

**What:** Comprehensive Prometheus metrics for monitoring election health and performance.

**Why:** operators need to know who's leader, how often elections occur, and whether something is wrong.

**Metrics Provided:**
- `election_is_leader` (gauge) — 1 if current instance is leader, 0 otherwise. Labels: `role`, `instance_id`, `bucket`
- `election_transitions_total` (counter) — Total number of state transitions. Labels: `role`, `instance_id`, `bucket`, `from_state`, `to_state`
- `election_failures_total` (counter) — Total number of election failures. Labels: `role`, `instance_id`, `bucket`, `error_type`
- `election_heartbeat_duration_seconds` (histogram) — Time taken for heartbeat operations. Labels: `role`, `instance_id`, `bucket`, `status`
- `election_leader_duration_seconds` (histogram) — How long instances hold leadership. Labels: `role`, `instance_id`, `bucket`
- `election_acquire_attempts_total` (counter) — Total election acquisition attempts. Labels: `role`, `instance_id`, `bucket`, `status`
- `election_token_validation_failures_total` (counter) — Token validation failures (indicates stale leaders). Labels: `role`, `instance_id`, `bucket`
- `election_connection_status` (gauge) — NATS connection status (1=connected, 0=disconnected). Labels: `role`, `instance_id`, `bucket`

**Structured Logging:**
- Use structured logging (e.g., `log/slog` or `zerolog`) with correlation IDs for tracing
- Log all state transitions with context
- Configurable log levels (DEBUG, INFO, WARN, ERROR)

### 6) Graceful shutdown & readiness

**What:** on SIGTERM, leader should optionally release leadership or mark itself as not ready and stop heartbeating to allow quick failover.

**Why:** reduce downtime during rolling upgrades.

**How:** provide `StopWithContext(ctx, opts)` that:
- Optionally deletes the KV key immediately (faster than waiting for TTL)
- Waits for `OnDemote` callback to complete (configurable timeout)
- Ensures all background goroutines are cleaned up
- Returns error if shutdown doesn't complete within timeout

**Example:**
```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

err := e.StopWithContext(ctx, leader.StopOptions{
    DeleteKey:      true,  // Delete key for fast failover
    WaitForDemote:  true,  // Wait for OnDemote to finish
    Timeout:        10 * time.Second,
})
```

### 7) gRPC task distribution (optional advanced)

**What:** let leader become an instruction source via a small gRPC control plane — followers subscribe and receive tasks.

**Why:** for complex orchestration, letting leader push assignments is easier than polling.

**How:** the library can provide a small `control` package to publish task messages to a subject or via gRPC with leader authentication using token.

---

## Usage example

```go
nc, _ := nats.Connect(nats.DefaultURL)
js, _ := nc.JetStream()
kv, _ := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "leaders"})

cfg := leader.ElectionConfig{
    Bucket: "leaders",
    Group:  "control-manager",
    InstanceID: myID,
    TTL: 5 * time.Second,
    HeartbeatInterval: 1 * time.Second,
}

e, err := leader.NewElection(nc, cfg)
if err != nil { log.Fatal(err) }

// register hooks
e.OnPromote(func(ctx context.Context, token string) {
    log.Println("promoted — starting leader tasks")
    go startLeaderTasks(ctx, token)
})

e.OnDemote(func() {
    log.Println("demoted — stopping leader tasks")
    stopLeaderTasks()
})

ctx := context.Background()
if err := e.Start(ctx); err != nil { log.Fatal(err) }

// run until shutdown
<-ctx.Done()
_ = e.Stop()
```

---

## Edge cases & pitfalls

* **Clock skew**: Don't rely on system clocks for ordering; use KV revision and tokens.
* **Network partitions**: Because NATS KV is RAFT-based, it tolerates partitions; but be cautious — if your client loses connectivity but remains running, ensure it demotes on lost heartbeats. The library monitors connection status and demotes if disconnected beyond `DisconnectGracePeriod`.
* **Split-brain**: Using `Create` + `Update` with expected revisions and fencing tokens greatly reduces this risk. Do not implement a takeover by `Put` without revision checks.
* **Thundering herd**: When a leader key expires, many followers may attempt `Create` at once. Use jittered backoff when retrying to reduce load. The library implements randomized jitter (50-200ms) and exponential backoff.
* **Unreliable KV TTL semantics**: Test TTL and heartbeat timing under load. Make heartbeat interval significantly smaller than TTL (minimum 3x ratio enforced) and consider performing a `kv.Update` before TTL/2.
* **NATS KV bucket deletion**: If the bucket is deleted while election is running, the library will detect this and return errors. Consider enabling `BucketAutoCreate` for development, but prefer explicit bucket management in production.
* **Error classification**: The library distinguishes between transient errors (network issues, temporary unavailability) and permanent errors (invalid config, permission denied). Transient errors trigger retries; permanent errors fail fast.
* **Connection health**: Monitor NATS connection status. If connection is lost, the library will demote after `DisconnectGracePeriod` to prevent stale leadership.

---

## Testing

**Unit Tests:**
- Mock `nats.Conn` and `nats.KeyValue` interfaces
- Provide test helpers that simulate KV state transitions (Create success/fail, Update success/fail, Watch events)
- Test error classification (transient vs. permanent)
- Test configuration validation
- Test retry and backoff logic

**Integration Tests:**
- Run a local nats-server (JetStream enabled) with a KV bucket
- Exercise leader takeover, demotion, and fencing
- Test with multiple candidates competing simultaneously
- Test connection loss and recovery scenarios

**Chaos Tests:**
- Simulate network drops and verify safe failover
- Simulate process stalls (GC pressure, CPU starvation)
- Simulate NATS server restarts
- Simulate clock skew (if possible)
- Test thundering herd scenarios (100+ followers competing)

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

## Roadmap / Future work

* CLI tool for debugging elections (list leaders, force-demote, inspect tokens)
* Official Prometheus exporter with advanced metrics
* `RoleManager` that manages many roles with single client connection
* gRPC control-plane scaffolding for task assignment
* Example operators for Kubernetes to show how to integrate with Pods when running inside cluster
* Optional integration with service discovery (consul / etcd) for multi-datacenter awareness
* **Observer mode**: Allow instances to watch elections without participating
* **Multi-datacenter support**: Leader election across multiple NATS clusters
* **Token signing**: Cryptographically signed tokens for stronger guarantees
* **Performance optimizations**: Connection pooling, batch operations

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
- **Cause**: Fencing token not being validated before operations
- **Solution**: Enable `FencingConfig.ValidateOnCriticalOps` or implement token validation in your leader tasks

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
- Enable token caching (`FencingConfig.CacheToken = true`)
- Set `FencingConfig.ValidationInterval` to reduce validation frequency

---

## Additional Resources

* [NATS JetStream Documentation](https://docs.nats.io/nats-concepts/jetstream)
* [Raft Consensus Algorithm](https://raft.github.io/)
* [Distributed Systems Patterns](https://martinfowler.com/articles/patterns-of-distributed-systems/)
