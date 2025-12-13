package leader

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
)

// Election is the main interface for participating in leader election.
//
// An Election instance represents a single participant in a leader election
// for a specific group/role. Multiple instances can participate in the same
// election, but only one will be leader at a time.
//
// Example usage:
//
//	cfg := ElectionConfig{
//	    Bucket:            "leaders",
//	    Group:             "control-manager",
//	    InstanceID:        "instance-1",
//	    TTL:               10 * time.Second,
//	    HeartbeatInterval: 1 * time.Second,
//	}
//	election, err := NewElectionWithConn(nc, cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	election.OnPromote(func(ctx context.Context, token string) {
//	    log.Println("Became leader, token:", token)
//	})
//
//	election.OnDemote(func() {
//	    log.Println("Lost leadership")
//	})
//
//	if err := election.Start(context.Background()); err != nil {
//	    log.Fatal(err)
//	}
//	defer election.Stop()
type Election interface {
	// Start begins participating in the election.
	// This method starts background goroutines for:
	//   - Attempting to acquire leadership
	//   - Watching for leader changes (if follower)
	//   - Sending heartbeats (if leader)
	//   - Validating tokens (if leader)
	//   - Monitoring connection health
	//
	// Start returns an error if:
	//   - The election is already started (ErrAlreadyStarted)
	//   - The configuration is invalid (ErrInvalidConfig)
	//   - The NATS connection cannot access JetStream
	//
	// The context is used for cancellation. If the context is cancelled,
	// the election will stop gracefully.
	//
	// Example:
	//   ctx := context.Background()
	//   if err := election.Start(ctx); err != nil {
	//       log.Fatal(err)
	//   }
	Start(ctx context.Context) error

	// Stop stops participating in the election and cleans up resources.
	// This method:
	//   - Stops all background goroutines
	//   - Does NOT delete the leadership key (if leader)
	//   - Does NOT wait for OnDemote callback
	//
	// For graceful shutdown with options, use StopWithContext instead.
	//
	// Stop returns an error if the election is already stopped (ErrAlreadyStopped).
	//
	// Example:
	//   if err := election.Stop(); err != nil {
	//       log.Printf("Error stopping election: %v", err)
	//   }
	Stop() error

	// StopWithContext stops the election with configurable options.
	// This method provides graceful shutdown capabilities:
	//   - Optional key deletion for fast failover
	//   - Optional waiting for OnDemote callback
	//   - Timeout handling via context
	//
	// Options:
	//   - DeleteKey: If true, deletes the leadership key immediately,
	//     allowing a new leader to be elected without waiting for TTL expiration.
	//   - WaitForDemote: If true, waits for the OnDemote callback to complete
	//     before returning. This ensures leader tasks are stopped before shutdown.
	//   - Timeout: Maximum time to wait. If 0, uses context timeout or 5s default.
	//
	// Example:
	//   ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	//   defer cancel()
	//   err := election.StopWithContext(ctx, StopOptions{
	//       DeleteKey:      true,
	//       WaitForDemote:  true,
	//   })
	StopWithContext(ctx context.Context, opts StopOptions) error

	// Status returns the current status of the election.
	// This includes:
	//   - Current state (INIT, CANDIDATE, LEADER, FOLLOWER, DEMOTED, STOPPED)
	//   - Whether this instance is the leader
	//   - Current leader ID
	//   - Fencing token (if leader)
	//   - Last heartbeat time (if leader)
	//   - Last state transition time
	//   - Current KV revision (if leader)
	//   - NATS connection status
	//
	// Example:
	//   status := election.Status()
	//   if status.IsLeader {
	//       log.Printf("I am the leader, token: %s", status.Token)
	//   } else {
	//       log.Printf("Current leader: %s", status.LeaderID)
	//   }
	Status() ElectionStatus

	// IsLeader returns true if this instance currently holds leadership.
	// This is a convenience method equivalent to Status().IsLeader.
	//
	// Example:
	//   if election.IsLeader() {
	//       performLeaderTasks()
	//   }
	IsLeader() bool

	// LeaderID returns the instance ID of the current leader.
	// Returns empty string if no leader is known.
	// This may be this instance's ID (if leader) or another instance's ID (if follower).
	//
	// Example:
	//   leaderID := election.LeaderID()
	//   if leaderID == "" {
	//       log.Println("No leader known")
	//   } else if leaderID == myInstanceID {
	//       log.Println("I am the leader")
	//   } else {
	//       log.Printf("Leader is: %s", leaderID)
	//   }
	LeaderID() string

	// Token returns the current fencing token for this instance if it is the leader.
	// Returns empty string if not leader.
	//
	// The fencing token is a UUID that uniquely identifies the current leadership.
	// It should be validated before performing critical operations to ensure
	// this instance is still the leader.
	//
	// Example:
	//   if election.IsLeader() {
	//       token := election.Token()
	//       // Use token for fencing in external systems
	//   }
	Token() string

	// OnPromote registers a callback that is executed when this instance becomes leader.
	// The callback receives:
	//   - A context that is cancelled when leadership is lost
	//   - The fencing token for this leadership term
	//
	// The callback is called in a separate goroutine, so it should not block.
	// If the callback panics, the panic is caught and logged, but does not
	// affect the election process.
	//
	// Multiple calls to OnPromote replace the previous callback.
	//
	// Example:
	//   election.OnPromote(func(ctx context.Context, token string) {
	//       log.Printf("Became leader with token: %s", token)
	//       go startLeaderTasks(ctx, token)
	//   })
	OnPromote(func(ctx context.Context, token string))

	// OnDemote registers a callback that is executed when this instance loses leadership.
	// The callback is called in a separate goroutine, so it should not block.
	// If the callback panics, the panic is caught and logged, but does not
	// affect the election process.
	//
	// Multiple calls to OnDemote replace the previous callback.
	//
	// Example:
	//   election.OnDemote(func() {
	//       log.Println("Lost leadership")
	//       stopLeaderTasks()
	//   })
	OnDemote(func())

	// ValidateToken validates the current token against the KV store.
	// Returns true if the token is valid (this instance is still leader),
	// false if invalid (this instance is no longer leader or not leader).
	//
	// This method performs a synchronous check by reading from the KV store.
	// For periodic validation, use ValidationInterval in ElectionConfig instead.
	//
	// Example:
	//   valid, err := election.ValidateToken(ctx)
	//   if err != nil {
	//       log.Printf("Error validating token: %v", err)
	//   } else if !valid {
	//       log.Println("Token invalid - no longer leader")
	//   }
	ValidateToken(ctx context.Context) (bool, error)

	// ValidateTokenOrDemote validates the token and demotes if invalid.
	// Returns true if the token is valid, false if invalid (and demoted).
	//
	// This is a convenience method that combines ValidateToken with automatic
	// demotion. Use this before critical operations to ensure this instance
	// is still the leader.
	//
	// Example:
	//   if !election.ValidateTokenOrDemote(ctx) {
	//       log.Println("Token invalid, operation aborted")
	//       return
	//   }
	//   // Perform critical operation...
	ValidateTokenOrDemote(ctx context.Context) bool
}

// ElectionConfig configures an election instance.
//
// Required fields must be set. Optional fields have sensible defaults.
// The configuration is validated when creating an election instance.
//
// Example:
//
//	cfg := ElectionConfig{
//	    // Required
//	    Bucket:            "leaders",
//	    Group:             "control-manager",
//	    InstanceID:        "instance-1",
//	    TTL:               10 * time.Second,
//	    HeartbeatInterval: 1 * time.Second,
//
//	    // Optional
//	    ValidationInterval: 5 * time.Second,
//	    Logger:            myLogger,
//	    Metrics:           myMetrics,
//	}
type ElectionConfig struct {
	// Bucket is the name of the NATS KV bucket to use for leadership storage.
	// The bucket must exist before creating an election instance.
	// Example: "leaders", "elections", "my-app-leaders"
	Bucket string

	// Group is the election group/role identifier.
	// Multiple instances with the same Group participate in the same election.
	// Different Groups represent independent elections (multi-role support).
	// Example: "control-manager", "scheduler", "coordinator"
	Group string

	// InstanceID is a unique identifier for this instance.
	// Must be unique across all instances participating in the same election.
	// Typically: hostname, pod name, container ID, or UUID.
	// Example: "instance-1", "pod-abc123", "host-192.168.1.1"
	InstanceID string

	// TTL is the time-to-live for the leadership key in the KV store.
	// The key will expire if not refreshed within this duration.
	// Must be >= HeartbeatInterval * 3 (enforced by validation).
	// Recommended: 3-5x HeartbeatInterval for safety margin.
	// Example: 10 * time.Second (with HeartbeatInterval: 1 * time.Second)
	TTL time.Duration

	// HeartbeatInterval is how often the leader refreshes the leadership key.
	// Must be < TTL / 3 (enforced by validation).
	// Shorter intervals provide faster failover but more KV operations.
	// Recommended: 1-2 seconds for most use cases.
	// Example: 1 * time.Second
	HeartbeatInterval time.Duration

	// Logger is an optional structured logger for observability.
	// If nil, logging is disabled (no-op logger).
	// Implementations should use zap.Field for structured data.
	// Example: zap.NewProduction(), zap.NewDevelopment()
	Logger Logger

	// ValidationInterval is how often to validate the fencing token in the background.
	// If 0, background validation is disabled (not recommended for production).
	// Recommended: 5-10 seconds for most use cases.
	// Example: 5 * time.Second
	ValidationInterval time.Duration

	// DisconnectGracePeriod is how long to wait before demoting on NATS disconnect.
	// If 0, uses default: max(3 * HeartbeatInterval, 5 seconds).
	// During this period, the leader continues to act as leader, allowing
	// for brief network interruptions without triggering failover.
	// Example: 5 * time.Second
	DisconnectGracePeriod time.Duration

	// Metrics is an optional metrics recorder for observability.
	// If nil, metrics recording is disabled.
	// Use NewPrometheusMetrics() to create a Prometheus implementation.
	// Example: leader.NewPrometheusMetrics(prometheus.DefaultRegisterer)
	Metrics Metrics

	// HealthChecker is an optional health checker for self-monitoring.
	// If nil, health checking is disabled.
	// If provided, health is checked before each heartbeat.
	// If health check fails MaxConsecutiveFailures times consecutively,
	// the leader will automatically demote itself.
	// Example: &MyHealthChecker{}
	HealthChecker HealthChecker

	// MaxConsecutiveFailures is the maximum number of consecutive health check
	// failures before automatic demotion.
	// Only used if HealthChecker is set.
	// Default: 3
	// Example: 3
	MaxConsecutiveFailures int

	// Priority is used for leader selection when AllowPriorityTakeover is true.
	// Higher number = higher priority.
	// Default: 0 (no priority, first-come-first-served).
	// Must be > 0 if AllowPriorityTakeover is true (enforced by validation).
	// Example: 10 (lower priority), 20 (higher priority)
	Priority int

	// AllowPriorityTakeover enables priority-based preemption.
	// If true, a higher-priority instance can take over from a lower-priority leader.
	// If false, priority is ignored and first-come-first-served applies.
	// Default: false (disabled for safety).
	//
	// WARNING: Priority takeover can cause service disruption. Use with caution.
	// Prefer voluntary demotion (health checks, graceful shutdown) over forced preemption.
	//
	// Use cases:
	//   - Primary/backup datacenter scenarios
	//   - Maintenance mode (lower priority to allow takeover)
	//   - Clear priority hierarchy
	//
	// Example: true (enable priority takeover)
	AllowPriorityTakeover bool
}

// StopOptions configures the behavior of StopWithContext.
//
// Example:
//
//	opts := StopOptions{
//	    DeleteKey:      true,  // Fast failover
//	    WaitForDemote:  true,  // Wait for cleanup
//	    Timeout:        10 * time.Second,
//	}
//	err := election.StopWithContext(ctx, opts)
type StopOptions struct {
	// DeleteKey deletes the leadership key on stop for fast failover.
	// If true, the key is deleted immediately, allowing a new leader
	// to be elected without waiting for TTL expiration.
	// If false, the key remains until TTL expires (slower failover).
	// Recommended: true for graceful shutdowns, false for crashes.
	DeleteKey bool

	// WaitForDemote waits for the OnDemote callback to complete before
	// returning. If true, ensures leader tasks are stopped before shutdown.
	// If false, the callback is called but not waited for (faster shutdown).
	// Recommended: true for graceful shutdowns.
	WaitForDemote bool

	// Timeout is the maximum time to wait for shutdown operations.
	// If 0, the context timeout is used. If context has no timeout,
	// a default of 5 seconds is used.
	// Recommended: 10-30 seconds depending on cleanup time.
	Timeout time.Duration
}

// JetStreamProvider is an interface for accessing NATS JetStream.
// This allows the library to work with different NATS connection types
// and enables testing with mocks.
//
// The nats.Conn type implements this interface via its JetStream() method.
type JetStreamProvider interface {
	JetStream() (JetStreamContext, error)
}

// NATSConnectionProvider provides access to the underlying NATS connection
// for connection monitoring. This is optional - if not implemented,
// connection monitoring will be disabled.
//
// The nats.Conn type can be wrapped to implement this interface.
// Connection monitoring enables automatic demotion on disconnect.
type NATSConnectionProvider interface {
	NATSConnection() *nats.Conn
}

// Entry represents a key-value entry in the KV store.
// This is an abstraction over NATS KV entries to enable testing.
type Entry interface {
	Key() string
	Value() []byte
	Revision() uint64
}

// Watcher watches for changes to a key in the KV store.
// This is an abstraction over NATS KV watchers to enable testing.
type Watcher interface {
	Updates() <-chan Entry
	Stop()
}

// KeyValue is an abstraction over NATS KV operations.
// This interface enables testing with mocks.
type KeyValue interface {
	Create(key string, value []byte, opts ...interface{}) (uint64, error)
	Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error)
	Get(key string) (Entry, error)
	Delete(key string) error
	Watch(key string, opts ...interface{}) (Watcher, error)
}

// JetStreamContext is an abstraction over NATS JetStream context.
// This interface enables testing with mocks.
type JetStreamContext interface {
	KeyValue(bucket string) (KeyValue, error)
}

type natsKeyValueAdapter struct {
	kv nats.KeyValue
}

func (a *natsKeyValueAdapter) Create(key string, value []byte, opts ...interface{}) (uint64, error) {
	// Note: NATS KV doesn't support per-key TTL options.
	// TTL is set at the bucket level when creating the bucket.
	// We accept TTL in opts for interface compatibility, but ignore it here.
	// The mock implementation can track TTL if needed for testing.
	return a.kv.Create(key, value)
}

func (a *natsKeyValueAdapter) Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error) {
	// Note: NATS KV doesn't support per-key TTL options.
	// TTL is set at the bucket level when creating the bucket.
	// We accept TTL in opts for interface compatibility, but ignore it here.
	// The mock implementation can track TTL if needed for testing.
	return a.kv.Update(key, value, rev)
}

func (a *natsKeyValueAdapter) Get(key string) (Entry, error) {
	natsEntry, err := a.kv.Get(key)
	if err != nil {
		return nil, err
	}
	if natsEntry == nil {
		return nil, nil
	}
	return &natsEntryAdapter{entry: natsEntry}, nil
}

func (a *natsKeyValueAdapter) Delete(key string) error {
	return a.kv.Delete(key)
}

func (a *natsKeyValueAdapter) Watch(key string, opts ...interface{}) (Watcher, error) {
	natsWatcher, err := a.kv.Watch(key)
	if err != nil {
		return nil, err
	}
	return &natsWatcherAdapter{watcher: natsWatcher}, nil
}

type natsWatcherAdapter struct {
	watcher nats.KeyWatcher
}

func (a *natsWatcherAdapter) Updates() <-chan Entry {
	entryChan := make(chan Entry, 1)
	go func() {
		defer close(entryChan)
		for natsEntry := range a.watcher.Updates() {
			if natsEntry != nil {
				entryChan <- &natsEntryAdapter{entry: natsEntry}
			} else {
				entryChan <- nil
			}
		}
	}()
	return entryChan
}

func (a *natsWatcherAdapter) Stop() {
	_ = a.watcher.Stop()
}

type natsEntryAdapter struct {
	entry nats.KeyValueEntry
}

func (a *natsEntryAdapter) Key() string {
	return a.entry.Key()
}

func (a *natsEntryAdapter) Value() []byte {
	return a.entry.Value()
}

func (a *natsEntryAdapter) Revision() uint64 {
	return a.entry.Revision()
}

type natsJetStreamAdapter struct {
	js nats.JetStreamContext
}

func (a *natsJetStreamAdapter) KeyValue(bucket string) (KeyValue, error) {
	kv, err := a.js.KeyValue(bucket)
	if err != nil {
		return nil, err
	}
	return &natsKeyValueAdapter{kv: kv}, nil
}

type natsConnAdapter struct {
	nc *nats.Conn
}

func (a *natsConnAdapter) JetStream() (JetStreamContext, error) {
	js, err := a.nc.JetStream()
	if err != nil {
		return nil, err
	}
	return &natsJetStreamAdapter{js: js}, nil
}

// NewElection creates a new election instance with a JetStream provider.
// This is the low-level constructor that accepts any JetStreamProvider.
// For most use cases, use NewElectionWithConn instead.
//
// The configuration is validated before creating the election instance.
// Returns an error if:
//   - Configuration is invalid (ErrInvalidConfig)
//   - JetStream is not available
//
// Example:
//
//	adapter := &myJetStreamProvider{}
//	election, err := NewElection(adapter, cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
func NewElection(nc JetStreamProvider, cfg ElectionConfig) (Election, error) {
	return newKVElection(nc, cfg)
}

// NewElectionWithConn creates a new election instance with a NATS connection.
// This is a convenience function that wraps the connection.
//
// The configuration is validated before creating the election instance.
// Returns an error if:
//   - Configuration is invalid (ErrInvalidConfig)
//   - JetStream is not available
//
// Example:
//
//	nc, err := nats.Connect(nats.DefaultURL)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	election, err := NewElectionWithConn(nc, cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
func NewElectionWithConn(nc *nats.Conn, cfg ElectionConfig) (Election, error) {
	return NewElection(&natsConnAdapter{nc: nc}, cfg)
}

// ElectionStatus contains the current status of an election instance.
// Use Election.Status() to get the current status.
//
// Example:
//
//	status := election.Status()
//	log.Printf("State: %s, IsLeader: %v, LeaderID: %s",
//	    status.State, status.IsLeader, status.LeaderID)
type ElectionStatus struct {
	// State is the current state of the election.
	// Possible values: INIT, CANDIDATE, LEADER, FOLLOWER, DEMOTED, STOPPED
	State string

	// IsLeader is true if this instance is currently the leader.
	IsLeader bool

	// LeaderID is the instance ID of the current leader.
	// Empty if no leader is known.
	LeaderID string

	// Token is the fencing token for this instance if it is the leader.
	// Empty if not leader.
	Token string

	// LastHeartbeat is the time of the last successful heartbeat (if leader).
	// Zero if not leader or no heartbeat yet.
	LastHeartbeat time.Time

	// LastTransition is the time of the last state transition.
	LastTransition time.Time

	// Revision is the current KV revision (if leader).
	// Zero if not leader.
	Revision uint64
}
