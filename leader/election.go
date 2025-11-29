package leader

import (
	"context"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/utils/logger"
	"github.com/nats-io/nats.go"
)

type Election interface {
	// Start begins participating in the election (runs in background goroutines).
	Start(ctx context.Context) error

	// Stop stops election participation and cleans up background goroutines.
	Stop() error

	// StopWithContext stops election participation with a timeout context.
	// TODO: Add StopOptions parameter in Phase 2
	StopWithContext(ctx context.Context) error

	// Status returns detailed status information about the election.
	Status() ElectionStatus

	// IsLeader returns true if this instance currently holds leadership.
	IsLeader() bool

	// LeaderID returns the current leader instance ID (may be empty if unknown).
	LeaderID() string

	// Token returns the current fencing token for this instance if leader.
	Token() string

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

	// Optional fields (can be nil/zero for now)
	Logger logger.Logger // Structured logger (nil = no logging)

	// TODO: Add in Phase 2 - Advanced features
	// Priority              int           // Priority for takeover (higher = preferred)
	// ConnectionTimeout     time.Duration // Timeout for KV operations
	// DisconnectGracePeriod time.Duration // How long to wait before demoting on disconnect
	// Fencing               FencingConfig // Token validation strategy
	// HealthChecker         HealthChecker // Optional health check callback
	// RetryConfig           RetryConfig   // Retry strategy for transient failures
	// BucketAutoCreate      bool          // Create bucket if it doesn't exist
	// DeleteOnStop          bool          // Delete key on Stop() for fast failover
}

// NewElection creates a new Election instance with the given NATS connection and configuration.
// The NATS connection must be connected and JetStream must be enabled.
// TODO: Will be fully implemented in Step 3
func NewElection(nc *nats.Conn, cfg ElectionConfig) (Election, error) {
	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// TODO: Create and return kvElection implementation in Step 3
	return nil, nil
}

// validateConfig validates the ElectionConfig.
// TODO: Implement in validation.go (Phase 1, Step 10)
func validateConfig(cfg ElectionConfig) error {
	// Basic validation - will be expanded later
	if cfg.Bucket == "" {
		return ErrInvalidConfig
	}
	if cfg.Group == "" {
		return ErrInvalidConfig
	}
	if cfg.InstanceID == "" {
		return ErrInvalidConfig
	}
	if cfg.TTL <= 0 {
		return ErrInvalidConfig
	}
	if cfg.HeartbeatInterval <= 0 {
		return ErrInvalidConfig
	}
	if cfg.TTL < cfg.HeartbeatInterval*3 {
		return ErrInvalidConfig
	}
	return nil
}

type ElectionStatus struct {
	State          string    // Current state: INIT, CANDIDATE, LEADER, FOLLOWER, DEMOTED, STOPPED
	IsLeader       bool      // Convenience: true if state == LEADER
	LeaderID       string    // Current leader instance ID
	Token          string    // Current fencing token (if leader)
	LastHeartbeat  time.Time // Last successful heartbeat (if leader)
	LastTransition time.Time // When state last changed
	Revision       uint64    // Current KV revision (if leader)
	// TODO: Add ConnectionStatus in Phase 2
}

// FencingConfig configures fencing token validation strategy.
// TODO: Will be used in Phase 2
type FencingConfig struct {
	// ValidationInterval: how often to validate token in background (0 = disabled)
	ValidationInterval time.Duration

	// ValidateOnCriticalOps: always validate token before critical operations
	ValidateOnCriticalOps bool

	// CacheToken: cache token locally and validate periodically (default: true)
	CacheToken bool
}

// RetryConfig configures retry and backoff strategy for transient failures.
// TODO: Will be used in Phase 2
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

// StopOptions configures graceful shutdown behavior.
// TODO: Will be used in Phase 2
type StopOptions struct {
	// DeleteKey: delete the leadership key on stop (enables fast failover)
	DeleteKey bool

	// WaitForDemote: wait for OnDemote callback to complete
	WaitForDemote bool

	// Timeout: maximum time to wait for graceful shutdown
	Timeout time.Duration
}

// HealthChecker is an interface for health checking.
// TODO: Will be used in Phase 2
type HealthChecker interface {
	// Check returns true if the instance is healthy and should retain leadership
	Check(ctx context.Context) bool
}
