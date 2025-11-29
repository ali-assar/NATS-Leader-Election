# Project Plan — nats-leader-election

This document bundles everything you asked for: a project roadmap, a full implementation skeleton (file layout + key code snippets), README examples to copy-paste, and naming/branding + logo suggestions. Treat this as the single source-of-truth for the next phase of work.

---

## 1 — Roadmap (milestones & deliverables)

> Timeline estimates are suggestions for planning only. You control pacing.

### Phase 0 — Kickoff (1–3 days)

* Create repo skeleton, LICENSE, CONTRIBUTING, CODE_OF_CONDUCT
* Add README (already created) and design.md (done)
* Add CI skeleton (GitHub Actions) for `go test` and `golangci-lint`

**Deliverables:** Repo skeleton, CI pipeline

### Phase 1 — Core library (1–2 weeks)

* Implement `Election` interface and `kv` backed implementation
* Heartbeat loop with conditional Update + revision tracking
* Watcher for followers
* OnPromote/OnDemote hooks
* Basic unit tests with mocked `nats.KeyValue`

**Deliverables:** `leader` package, unit tests, API docs

### Phase 2 — Robustness & features (1–2 weeks)

* Fencing tokens fully implemented and tested
  - Periodic background validation
  - Operation-level validation
  - Token caching with refresh
* HealthChecker and graceful shutdown logic
  - `StopWithContext` with timeout
  - Optional key deletion on stop
  - Wait for OnDemote callback completion
* Connection health monitoring
  - NATS connection status tracking
  - Disconnect grace period handling
  - Reconnection verification
* Jitter/backoff strategy for followers
  - Exponential backoff with jitter
  - Circuit breaker pattern
  - Error classification (transient vs permanent)
* Configurable logging and Prometheus metrics
  - Structured logging with correlation IDs
  - Comprehensive metrics (see readme.md for full list)
* Configuration validation
  - Validate TTL/HeartbeatInterval ratio
  - Validate required fields
  - Validate timeout relationships

**Deliverables:** Improved tests (integration with local nats-server), metrics, health integration, connection management

### Phase 3 — Multi-role, examples, docs (1 week)

* `RoleManager` convenience API for multiple roles
* Example apps: simple control manager, multi-role demo, K8s integration example
* README quickstart / examples section updated

**Deliverables:** Example apps, updated README

### Phase 4 — Packaging & outreach (1–2 weeks)

* Release v0.1.0, create GitHub release notes
* Publish a blog post / HN / Reddit announcement + NATS Slack share
* Add example CI for releasing to GitHub packages (optional)

**Deliverables:** v0.1.0 release, announcement materials

### Phase 5 — Advanced features & polish (optional, future)

* CLI tool for debugging elections
* Observer mode (watch elections without participating)
* Multi-datacenter support
* Token signing for stronger guarantees
* Performance optimizations (connection pooling, batch operations)
* Comprehensive documentation (troubleshooting guide, performance tuning guide)

**Deliverables:** CLI tool, advanced features, enhanced documentation

---

## 4 — Testing strategy

### Unit Tests

* Mock `nats.Conn` and `nats.KeyValue` interfaces
* Test all state transitions
* Test error classification
* Test configuration validation
* Test retry and backoff logic
* Test fencing token validation

### Integration Tests

* Use real NATS server (JetStream enabled)
* Test with multiple candidates
* Test leader takeover scenarios
* Test connection loss and recovery
* Test graceful shutdown

### Chaos Tests

* Network partition simulation
* Process kill simulation
* NATS server restart
* Clock skew simulation (if possible)
* Thundering herd (100+ followers)

### Property-Based Tests

* Use `gopter` or similar library
* Test state machine invariants
* Test edge cases in transitions

### Load Tests

* Many concurrent candidates
* High-frequency transitions
* Long-running elections

---

## 2 — Implementation skeleton (files, packages, and key snippets)

```
nats-leader-election/
├─ cmd/
│  └─ demo/
│     └─ main.go          # demo app to show library usage
├─ leader/
│  ├─ election.go        # public API and types
│  ├─ kv_election.go     # kv-backed implementation
│  ├─ heartbeat.go       # heartbeat logic
│  ├─ watcher.go         # KV watch logic
│  ├─ fencing.go         # fencing token validation
│  ├─ connection.go      # connection health monitoring
│  ├─ retry.go           # retry and backoff logic
│  ├─ validation.go      # configuration validation
│  ├─ role_manager.go    # optional multi-role manager
│  ├─ health.go          # health-checker integration
│  ├─ metrics.go         # prometheus metrics
│  └─ errors.go          # error types and classification
├─ internal/
│  └─ natsmock/          # test mocks for nats.KeyValue and nats.Conn
├─ examples/
│  ├─ control-manager/   # small app that uses leader
│  └─ multi-role/
├─ scripts/
│  └─ start-nats.sh
├─ .github/
│  └─ workflows/ci.yml
├─ go.mod
├─ README.md
└─ design.md
```

### 2.1 `election.go` (public API)

```go
package leader

import "context"

type Election interface {
    Start(ctx context.Context) error
    Stop() error
    IsLeader() bool
    LeaderID() string
    Token() string
    OnPromote(func(ctx context.Context, token string))
    OnDemote(func())
}

type ElectionConfig struct {
    // Required fields
    Bucket            string
    Group             string
    InstanceID        string
    TTL               time.Duration
    HeartbeatInterval time.Duration
    
    // Optional fields
    Priority          int
    Logger            Logger
    
    // Connection management
    ConnectionTimeout     time.Duration
    DisconnectGracePeriod time.Duration
    
    // Fencing configuration
    Fencing FencingConfig
    
    // Health checking
    HealthChecker HealthChecker
    
    // Retry and backoff
    RetryConfig RetryConfig
    
    // Bucket management
    BucketAutoCreate bool
    
    // Graceful shutdown
    DeleteOnStop bool
}

type FencingConfig struct {
    ValidationInterval     time.Duration
    ValidateOnCriticalOps  bool
    CacheToken             bool
}

type RetryConfig struct {
    MaxAttempts      int
    InitialBackoff   time.Duration
    MaxBackoff       time.Duration
    BackoffMultiplier float64
    Jitter           float64
}

func NewElection(nc *nats.Conn, cfg ElectionConfig) (Election, error) {
    // returns kv-backed implementation
}
```

### 2.2 `kv_election.go` (core outline)

* Holds `kv nats.KeyValue`, `instanceID`, `token`, `ctx`, `cancel`, `isLeader atomic.Bool`, `rev uint64`
* Implements Start: attempts Create, spawns heartbeat and watcher
* Implements Stop: cancels context, stops heartbeat, optionally deletes key

Key functions to implement:

* `attemptAcquire()` — tries `kv.Create(key, payload)` and sets state on success
* `heartbeatLoop()` — does `kv.Update(key, payload, expectedRevision)` regularly
* `watchLoop()` — waits on `kv.Watch` and signals create attempts on delete

### 2.3 `heartbeat.go`

Pseudocode for heartbeat loop with timeout and error handling:

```go
func (e *kvElection) heartbeatLoop(ctx context.Context) {
    ticker := time.NewTicker(e.cfg.HeartbeatInterval)
    defer ticker.Stop()
    
    consecutiveFailures := 0
    maxFailures := 3
    
    for {
        select {
        case <-ctx.Done(): 
            return
        case <-ticker.C:
            if !e.IsLeader() { 
                consecutiveFailures = 0
                continue 
            }
            
            // Check health before heartbeat
            if e.cfg.HealthChecker != nil {
                if !e.cfg.HealthChecker.Check(ctx) {
                    e.handleHealthCheckFailure()
                    return
                }
            }
            
            // Update with expected revision and timeout
            hbCtx, cancel := context.WithTimeout(ctx, e.cfg.ConnectionTimeout)
            rev, err := e.kv.Update(e.groupKey, payload, nats.ExpectedRevision(e.rev))
            cancel()
            
            if err != nil {
                consecutiveFailures++
                if isPermanentError(err) || consecutiveFailures >= maxFailures {
                    e.handleHeartbeatFailure(err)
                    return
                }
                // Transient error, will retry on next tick
                e.metrics.RecordHeartbeatFailure(err)
                continue
            }
            
            consecutiveFailures = 0
            e.rev = rev
            e.lastHeartbeat = time.Now()
            e.metrics.RecordHeartbeatSuccess(time.Since(hbCtx.Deadline()))
        }
    }
}
```

### 2.4 `watcher.go`

* Use `kv.Watch` to listen for `Delete` events or change events with expired TTL
* On delete → signal `attemptAcquire()` with jitter
* Implement exponential backoff for retries
* Handle watch errors and re-establish watch on failure

### 2.5 `fencing.go`

* Implement token validation logic
* Periodic background validation
* Operation-level validation
* Token caching with refresh

### 2.6 `connection.go`

* Monitor NATS connection status
* Track last successful operation
* Handle disconnection and reconnection
* Demote on extended disconnection

### 2.7 `retry.go`

* Error classification (transient vs permanent)
* Exponential backoff with jitter
* Circuit breaker pattern
* Max attempts limiting

### 2.8 `validation.go`

* Configuration validation
* TTL/HeartbeatInterval ratio check
* Required field validation
* Timeout relationship validation

### 2.9 `role_manager.go`

High-level convenience type that manages multiple `Election` instances and provides aggregated events (e.g., `OnRolePromote(role, token)`).

**Features:**
* Manage multiple roles with single NATS connection
* Atomic role acquisition (optional: all roles or none)
* Role dependencies (some roles depend on others)
* Role priority (different priorities per role)
* Aggregated status and metrics

---

## 3 — Examples for README (copy/paste)

### Quickstart (short)

````md
### Quickstart

```go
nc, _ := nats.Connect(nats.DefaultURL)

cfg := leader.ElectionConfig{
  Bucket: "leaders",
  Group:  "control-manager",
  InstanceID: "node-01",
  TTL: 5 * time.Second,
  HeartbeatInterval: 1 * time.Second,
}

e, _ := leader.NewElection(nc, cfg)
e.OnPromote(func(ctx context.Context, token string){
  // Start leader-only work
})
e.OnDemote(func(){
  // Stop leader-only work
})

ctx := context.Background()
e.Start(ctx)
````

````

### Multi-role example

```md
// Create multiple roles
roles := []string{"scheduler","cleanup"}
for _, r := range roles {
  cfg.Group = r
  e, _ := leader.NewElection(nc, cfg)
  // register hooks per role
  e.Start(ctx)
}
````

### Graceful shutdown example

```go
// on SIGTERM
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

err := e.StopWithContext(ctx, leader.StopOptions{
    DeleteKey:     true,  // Delete key for fast failover
    WaitForDemote: true,  // Wait for OnDemote to finish
    Timeout:       10 * time.Second,
})
if err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

### Connection health monitoring example

```go
cfg := leader.ElectionConfig{
    // ... other config
    DisconnectGracePeriod: 5 * time.Second,
    ConnectionTimeout:     2 * time.Second,
}

// Status() will show connection status
status := e.Status()
if status.ConnectionStatus == "DISCONNECTED" {
    log.Printf("NATS connection lost, will demote if not restored within %v", cfg.DisconnectGracePeriod)
}
```

### Fencing token validation example

```go
cfg := leader.ElectionConfig{
    // ... other config
    Fencing: leader.FencingConfig{
        ValidationInterval:    5 * time.Second,
        ValidateOnCriticalOps: true,
        CacheToken:            true,
    },
}

// In your leader tasks, validate before critical operations
if e.IsLeader() {
    if !e.ValidateToken() {
        // Token invalid, demotion will happen automatically
        return
    }
    // Proceed with critical operation
}
```

---