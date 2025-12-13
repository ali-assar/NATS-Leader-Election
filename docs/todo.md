# TODO — NATS Leader Election Project

This is your step-by-step learning guide. Work through each task in order, understanding the concepts as you go. Don't rush—take time to understand each step before moving to the next.

---

## Phase 0: Project Setup & Foundation (Days 1-3)

### Setup Tasks

- [x] **0.1** Review the documentation
  - Read `docs/readme.md` to understand the project goals
  - Read `docs/design.md` to understand the architecture
  - Read `docs/plan.md` to see the implementation plan
  - **Learning**: Understand what leader election is and why we need it

- [x] **0.2** Set up Go workspace
  - Verify Go is installed: `go version`
  - Initialize module if needed: `go mod tidy`
  - Create directory structure: `mkdir -p leader internal/natsmock examples`
  - **Learning**: Understand Go module structure and package organization

- [x] **0.3** Set up development tools
  - Install `golangci-lint`: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
  - Set up your IDE/editor (VS Code, GoLand, etc.)
  - Configure Go formatting on save
  - **Learning**: Good development practices and tooling

- [x] **0.4** Set up NATS for testing
  - Install NATS server: `go install github.com/nats-io/nats-server/v2@latest`
  - Test NATS server: `nats-server -js` (JetStream enabled)
  - Create a simple test script to verify NATS connection
  - **Learning**: Understanding NATS and JetStream basics

- [ ] **0.5** Set up CI/CD skeleton
  - Create `.github/workflows/ci.yml`
  - Add basic Go test and lint steps
  - Test the workflow
  - **Learning**: Continuous integration concepts

---

## Phase 1: Core Library Implementation (Weeks 1-2)

### Step 1: Define Core Types and Interfaces

- [x] **1.1** Create `leader/errors.go`
  - Define custom error types: `ErrNotLeader`, `ErrElectionFailed`, etc.
  - **Learning**: Error handling patterns in Go, custom error types

- [x] **1.2** Create `leader/election.go` - Part 1: Interfaces
  - Define `Election` interface with all methods
  - Define `ElectionConfig` struct with required fields
  - Add basic documentation comments
  - **Learning**: Interface design, API design principles

- [x] **1.3** Create `leader/election.go` - Part 2: Configuration
  - Add optional fields to `ElectionConfig`
  - Create helper types: `FencingConfig`, `RetryConfig`, `StopOptions`
  - Add `ElectionStatus` struct
  - **Learning**: Configuration patterns, optional parameters

- [x] **1.4** Write tests for configuration validation
  - Create `leader/election_test.go`
  - Test invalid configurations (TTL too short, empty InstanceID, etc.)
  - **Learning**: Test-driven development, validation logic

### Step 2: Create Mock Infrastructure for Testing

- [x] **2.1** Create `internal/natsmock/keyvalue.go`
  - Create mock `KeyValue` interface implementation
  - Implement `Create`, `Update`, `Get`, `Watch`, `Delete` methods
  - Add ability to simulate failures and delays
  - **Learning**: Mocking patterns, interface implementation

- [x] **2.2** Create `internal/natsmock/connection.go`
  - Create mock `Conn` interface implementation
  - Implement connection status tracking
  - Add ability to simulate disconnections
  - **Learning**: Mocking network connections, state simulation

- [x] **2.3** Write tests for mocks
  - Test mock behavior matches real NATS behavior
  - Test failure scenarios
  - **Learning**: Testing mocks, ensuring mock accuracy

### Step 3: Implement Core Election Logic

- [x] **3.1** Create `leader/kv_election.go` - Part 1: Structure
  - Create `kvElection` struct with all fields
  - Implement `NewElection` constructor
  - Add configuration validation
  - **Learning**: Struct design, constructor patterns, validation

- [x] **3.2** Implement `Start()` method
  - Initialize NATS KV connection
  - Create or get KV bucket
  - Set up initial state
  - **Learning**: Initialization patterns, error handling

- [x] **3.3** Implement `attemptAcquire()` method
  - Try to create the leadership key
  - Handle success (become leader) and failure (become follower)
  - Generate fencing token
  - **Learning**: Atomic operations, state transitions, UUID generation

- [x] **3.4** Write tests for `attemptAcquire()`
  - Test successful acquisition
  - Test failure when key exists
  - Test concurrent attempts
  - **Learning**: Testing concurrent code, race conditions

### Step 4: Implement Heartbeat Mechanism

- [x] **4.1** Create `leader/heartbeat.go` - Part 1: Structure
  - Create heartbeat loop function
  - Set up ticker for periodic heartbeats
  - **Learning**: Goroutines, tickers, background tasks

- [x] **4.2** Implement heartbeat update logic
  - Use `kv.Update()` with expected revision
  - Handle revision mismatches (demotion)
  - Update revision tracking
  - **Learning**: Conditional updates, revision tracking, conflict detection

- [x] **4.3** Add error handling to heartbeat
  - Classify errors (transient vs permanent)
  - Handle timeouts
  - Implement failure counting
  - **Learning**: Error classification, retry logic

- [x] **4.4** Write tests for heartbeat
  - Test successful heartbeats
  - Test revision mismatch (demotion)
  - Test timeout scenarios
  - **Learning**: Testing time-based logic, mocking time

### Step 5: Implement Watcher for Followers

- [x] **5.1** Create `leader/watcher.go` - Part 1: Structure
  - Create watcher loop function
  - Set up KV watch subscription
  - **Learning**: Event-driven programming, subscriptions

- [x] **5.2** Implement watch event handling
  - Handle key deletion events
  - Handle key update events
  - Trigger re-election attempts
  - **Learning**: Event processing, state change detection

- [x] **5.3** Add jitter and backoff to watcher
  - Implement random jitter before re-election
  - Add exponential backoff for retries
  - **Learning**: Backoff algorithms, jitter patterns, thundering herd prevention

- [x] **5.4** Write tests for watcher
  - Test watch event handling
  - Test jitter and backoff behavior
  - Test multiple followers competing
  - **Learning**: Testing async behavior, timing-sensitive code

- [x] **5.5** Prevent multiple watcher goroutines (Code Quality)
  - Add flag to track if watcher is already running
  - Prevent race condition when `becomeFollower()` is called multiple times
  - Ensure only one watcher loop runs per election instance
  - **Learning**: Race condition prevention, goroutine lifecycle management

### Step 6: Implement Callbacks and State Management

- [x] **6.1** Implement `OnPromote()` and `OnDemote()` callbacks
  - Store callback functions
  - Call callbacks at appropriate times
  - Handle callback errors gracefully
  - **Learning**: Callback patterns, function types, error handling

- [x] **6.2** Implement state management
  - Use atomic operations for `IsLeader()`
  - Track current leader ID
  - Track current token
  - **Learning**: Atomic operations, thread-safe state, race conditions

- [x] **6.3** Implement `Stop()` method
  - Cancel all goroutines
  - Clean up resources
  - Handle graceful shutdown
  - **Learning**: Resource cleanup, context cancellation, graceful shutdown

- [x] **6.4** Write integration tests
  - Test full election cycle (start → leader → demote → follower)
  - Test multiple instances competing
  - Test callbacks are called correctly
  - **Learning**: Integration testing, end-to-end scenarios

---

## Phase 2: Robustness & Advanced Features (Weeks 3-4)

### Step 7: Implement Fencing Tokens

- [x] **7.1** Create `leader/fencing.go` - Part 1: Token Management
  - Implement token generation and storage
  - Implement token caching
  - **Learning**: Token-based security, caching strategies

- [x] **7.2** Implement periodic token validation
  - Create background validation loop
  - Validate token against KV store
  - Handle validation failures (demotion)
  - **Learning**: Background validation, periodic checks

- [x] **7.3** Implement operation-level validation
  - Create `ValidateToken()` method
  - Add validation before critical operations
  - **Learning**: Pre-operation checks, safety mechanisms

- [x] **7.4** Write tests for fencing
  - Test token validation success
  - Test token validation failure (stale leader)
  - Test token caching
  - **Learning**: Testing security mechanisms, edge cases

### Step 8: Implement Connection Health Monitoring

- [x] **8.1** Create `leader/connection.go` - Part 1: Status Tracking
  - Subscribe to NATS connection status events
  - Track connection state
  - **Learning**: Event subscriptions, state tracking

- [x] **8.2** Implement disconnect handling
  - Detect disconnections
  - Start grace period timer
  - Demote if grace period exceeded
  - **Learning**: Network failure handling, grace periods

- [x] **8.3** Implement reconnection handling
  - Detect reconnections
  - Verify leadership status
  - Resume or demote based on verification
  - **Learning**: Reconnection logic, state verification

- [x] **8.4** Write tests for connection handling
  - Test disconnect scenarios
  - Test reconnection scenarios
  - Test grace period behavior
  - **Learning**: Testing network failure scenarios

### Step 9: Implement Retry and Backoff Logic

- [x] **9.1** Create `leader/retry.go` - Part 1: Error Classification
  - Implement error classification (transient vs permanent)
  - Create error type checking functions
  - Improve error wrapping to support Go 1.13+ `errors.Is()` and `errors.As()`
  - Make custom errors (like `timeoutError`) compatible with error wrapping conventions
  - **Learning**: Error analysis, error categorization, error wrapping patterns

- [x] **9.2** Implement exponential backoff
  - Create backoff calculation function
  - Add jitter to backoff
  - Implement max backoff limits
  - **Learning**: Backoff algorithms, exponential growth, jitter

- [x] **9.3** Implement circuit breaker pattern
  - Track consecutive failures
  - Open circuit after threshold
  - Reset circuit on success
  - **Learning**: Circuit breaker pattern, failure handling

- [x] **9.4** Write tests for retry logic
  - Test backoff calculations
  - Test circuit breaker behavior
  - Test error classification
  - **Learning**: Testing retry mechanisms, failure scenarios

### Step 10: Implement Configuration Validation

- [x] **10.1** Create `leader/validation.go`
  - Implement TTL/HeartbeatInterval ratio validation
  - Validate required fields
  - Validate timeout relationships
  - **Learning**: Input validation, constraint checking

- [x]*10.4** Implement TTL support in KV operations (Enhancement)
  - Add TTL support to `KeyValue` interface `Create()` method
  - Use `nats.WithMaxAge()` option when creating leadership key
  - Implement TTL in mock KeyValue for testing
  - Ensure TTL is properly set based on `ElectionConfig.TTL`
  - **Learning**: NATS KV TTL options, key expiration, testing with TTL

- [x] **10.2** Add validation to constructor
  - Call validation in `NewElection()`
  - Return clear error messages
  - **Learning**: Early validation, clear error messages

- [x] **10.3** Write tests for validation
  - Test all validation rules
  - Test error messages are clear
  - **Learning**: Testing validation logic

### Step 11: Implement Health Checker Integration

- [x] **11.1** Update `leader/health.go` (if exists) or create it
  - Define `HealthChecker` interface
  - Integrate health checks into heartbeat loop
  - **Learning**: Health checking patterns, integration points

- [x] **11.2** Implement health-based demotion
  - Check health before each heartbeat
  - Demote if health check fails N times
  - **Learning**: Health-aware systems, failure detection

- [x] **11.3** Write tests for health checking
  - Test healthy leader continues
  - Test unhealthy leader demotes
  - Test intermittent health issues
  - **Learning**: Testing health mechanisms

### Step 12: Implement Graceful Shutdown

- [x] **12.1** Implement `StopWithContext()` method
  - Accept context with timeout
  - Accept `StopOptions` for configuration
  - Ensure context is properly propagated to callbacks
  - Verify callback contexts are cancelled when election stops
  - **Learning**: Context patterns, timeout handling, options pattern, context propagation

- [x] **12.2** Implement key deletion on stop
  - Optionally delete leadership key
  - Handle deletion errors
  - **Learning**: Cleanup patterns, optional behavior

- [x] **12.3** Implement callback waiting
  - Wait for `OnDemote` to complete
  - Handle timeout scenarios
  - **Learning**: Synchronization, waiting for completion

- [x] **12.4** Write tests for graceful shutdown
  - Test key deletion
  - Test callback waiting
  - Test timeout scenarios
  - **Learning**: Testing shutdown behavior

### Step 13: Implement Observability (Logging & Metrics)

- [x] **13.1** Create `leader/logger.go` (or use existing logger interface)
  - Define logger interface
  - Implement structured logging
  - Add correlation IDs
  - **Learning**: Logging patterns, structured logging, tracing

- [x] **13.2** Add logging throughout the codebase
  - Log state transitions
  - Log errors with context
  - Log important events
  - Integrate existing logger interface from `utils/logger`
  - Add strategic log points in election lifecycle (start, stop, promote, demote)
  - **Learning**: Where and how to log, log levels, logger integration

- [x] **13.3** Create `leader/metrics.go` - Part 1: Structure
  - Define metrics interface
  - Set up Prometheus metrics
  - **Learning**: Metrics patterns, Prometheus integration

- [x] **13.4** Implement all metrics
  - `election_is_leader` (gauge)
  - `election_transitions_total` (counter)
  - `election_heartbeat_duration_seconds` (histogram)
  - All other metrics from readme.md
  - **Learning**: Different metric types, when to use each

- [x] **13.5** Write tests for metrics
  - Test metrics are recorded correctly
  - Test metric labels
  - **Learning**: Testing observability

---

## Phase 3: Integration & Examples (Week 5)

### Step 14: Create Integration Tests

- [x] **14.1** Set up integration test environment
  - Create test helper to start NATS server
  - Create test helper to create KV buckets
  - **Learning**: Test infrastructure, test helpers

- [x] **14.2** Write integration tests
  - Test full election with real NATS
  - Test leader takeover scenarios
  - Test multiple candidates
  - **Learning**: Integration testing, real system testing

- [x] **14.3** Write chaos tests
  - Test network partition scenarios
  - Test process kill scenarios
  - Test NATS server restart
  - **Learning**: Chaos engineering, failure testing

### Step 15: Create Example Applications

- [x] **15.1** Update `cmd/demo/main.go`
  - Create simple leader election demo
  - Show basic usage
  - Add comments explaining each step
  - **Learning**: Example code, documentation through code

- [x] **15.2** Create `examples/control-manager/`
  - Create a simple control manager example
  - Show leader-specific tasks
  - Show graceful shutdown
  - **Learning**: Real-world usage patterns

- [x] **15.3** Create `examples/multi-role/`
  - Show multiple role elections
  - Show role-specific callbacks
  - **Learning**: Multi-role patterns, complex scenarios

### Step 16: Implement RoleManager (Optional)

- [ ] **16.1** Create `leader/role_manager.go` - Part 1: Structure
  - Define `RoleManager` struct
  - Define `RoleManager` interface
  - **Learning**: Manager patterns, composition

- [ ] **16.2** Implement role management
  - Create multiple elections
  - Aggregate status
  - Handle role-specific callbacks
  - **Learning**: Managing multiple resources, aggregation

- [ ] **16.3** Write tests for RoleManager
  - Test multiple roles
  - Test role dependencies (if implemented)
  - **Learning**: Testing complex systems

---

## Phase 4: Polish & Documentation (Week 6)

### Step 17: Documentation

- [x] **17.1** Add Go doc comments
  - Document all public types and functions
  - Add usage examples in comments
  - **Learning**: Documentation best practices, Go doc

- [x] **17.2** Create API documentation
  - Generate godoc
  - Review and improve documentation
  - **Learning**: API documentation, user perspective

- [x] **17.3** Update README with examples
  - Add quickstart section
  - Add more usage examples
  - Add troubleshooting section
  - **Learning**: User documentation, examples

### Step 18: Code Quality

- [x] **18.1** Run linters and fix issues
  - Run `golangci-lint`
  - Fix all warnings and errors
  - **Learning**: Code quality tools, best practices

- [ ] **18.2** Review code for improvements
  - Look for code smells
  - Refactor if needed
  - Consolidate test helpers (`waitForCondition`, `waitForLeader`, `waitForHeartbeat`) into shared test helper file
  - Extract mock adapter code (`mockKeyValueAdapter`, `mockJetStreamAdapter`, `mockConnAdapter`) to reusable test helper package
  - **Learning**: Code review, refactoring, test code organization, DRY principles

- [ ] **18.3** Add performance benchmarks
  - Create benchmark tests
  - Measure and optimize if needed
  - **Learning**: Performance testing, optimization

### Step 19: Final Testing

- [ ] **19.1** Run full test suite
  - Unit tests
  - Integration tests
  - Chaos tests
  - **Learning**: Comprehensive testing

- [ ] **19.2** Test in different scenarios
  - Different TTL/heartbeat configurations
  - Different numbers of candidates
  - Different failure scenarios
  - **Learning**: Testing various scenarios

- [ ] **19.3** Load testing
  - Test with many concurrent candidates
  - Test high-frequency transitions
  - **Learning**: Load testing, performance under load

---

## Phase 5: Release Preparation (Week 7)

### Step 20: Release

- [ ] **20.1** Version tagging
  - Tag v0.1.0
  - Create release notes
  - **Learning**: Versioning, release management

- [ ] **20.2** Create GitHub release
  - Write release notes
  - Attach binaries (if applicable)
  - **Learning**: Release process

- [ ] **20.3** Share the project
  - Post on relevant forums
  - Share with NATS community
  - **Learning**: Open source community engagement

---

## Learning Resources

As you work through each step, refer to these resources:

### Go Concepts
- [ ] Go concurrency: goroutines, channels, sync primitives
- [ ] Go interfaces and type assertions
- [ ] Go error handling patterns
- [ ] Go testing (unit, integration, benchmarks)
- [ ] Go context package

### Distributed Systems
- [ ] Leader election algorithms
- [ ] Consensus algorithms (Raft)
- [ ] Distributed locking
- [ ] Fencing tokens
- [ ] Split-brain prevention

### NATS/JetStream
- [ ] NATS core concepts
- [ ] JetStream and KeyValue stores
- [ ] NATS connection management
- [ ] NATS error handling

### Best Practices
- [ ] Error handling
- [ ] Logging and observability
- [ ] Testing strategies
- [ ] Code organization
- [ ] API design

---

## Tips for Learning

1. **Don't skip steps**: Each step builds on previous knowledge
2. **Write tests first**: TDD helps you understand requirements
3. **Read the code**: Look at similar projects (etcd, Kubernetes leader election)
4. **Experiment**: Try breaking things to understand how they work
5. **Ask questions**: If stuck, research or ask for help
6. **Take breaks**: Complex concepts need time to sink in
7. **Review regularly**: Revisit previous steps to reinforce learning

---

## Progress Tracking

Track your progress here:

- Phase 0: 4/5 tasks completed (0.5 pending - CI/CD skeleton)
- Phase 1: 19/19 tasks completed ✅ (Steps 1-6 complete)
- Phase 2: 22/22 tasks completed ✅ (Steps 7-13 complete)
- Phase 3: 3/9 tasks completed (Step 14 complete - integration & chaos tests)
- Phase 4: 0/9 tasks completed
- Phase 5: 0/3 tasks completed

**Total: 48/67 tasks completed** (71% complete)

**Completed Features:**
- ✅ Core election logic with NATS KV
- ✅ Heartbeat mechanism with revision checking
- ✅ Watcher with periodic key checks (500ms interval)
- ✅ Fencing tokens with periodic validation
- ✅ Connection health monitoring with grace period
- ✅ Retry and backoff logic with circuit breaker
- ✅ Configuration validation
- ✅ Health checker integration
- ✅ Graceful shutdown with StopWithContext
- ✅ Observability (structured logging + Prometheus metrics)
- ✅ Integration tests with embedded NATS server
- ✅ Chaos tests (network partition, process kill, NATS restart, thundering herd)

---

## Notes Section

Use this space to track your learning notes, questions, and insights as you work through the project:

### Key Learnings
- 

### Questions to Research
- 

### Challenges Faced
- 

### Solutions Found
- 

---

**Good luck with your learning journey! 🚀**