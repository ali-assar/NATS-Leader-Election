package leader

import (
	"time"
)

// TimingConfig holds timing parameters for chaos test scenarios
type TimingConfig struct {
	// Configuration values
	TTL                    time.Duration
	HeartbeatInterval      time.Duration
	DisconnectGracePeriod  time.Duration
	ValidationInterval     time.Duration
	
	// Estimated operation delays
	InitialJitter          time.Duration // 10-100ms
	KVOperationDelay       time.Duration // ~10-50ms
	MaxRetryBackoff        time.Duration // up to 350ms (50ms + 100ms + 200ms)
	PeriodicCheckInterval  time.Duration // 500ms (worst case)
	NATSPropagationBuffer  time.Duration // ~500ms-1s for NATS propagation
}

// DefaultTimingConfig returns a default timing configuration
func DefaultTimingConfig() TimingConfig {
	return TimingConfig{
		InitialJitter:         100 * time.Millisecond,
		KVOperationDelay:      50 * time.Millisecond,
		MaxRetryBackoff:       350 * time.Millisecond,
		PeriodicCheckInterval: 500 * time.Millisecond,
		NATSPropagationBuffer: 1 * time.Second,
	}
}

// CalculateInitialAcquisitionTimeout calculates the timeout for initial leader acquisition
// This accounts for jitter, KV operations, and potential retries
func (tc TimingConfig) CalculateInitialAcquisitionTimeout() time.Duration {
	// Worst case: jitter + KV operation + retry backoff
	worstCase := tc.InitialJitter + tc.KVOperationDelay + tc.MaxRetryBackoff
	// Add buffer for real NATS and potential retries
	return worstCase + 2*time.Second
}

// CalculateNetworkPartitionTimeout calculates the timeout for network partition scenarios
// This accounts for grace period, TTL expiration, detection delays, and NATS propagation
func (tc TimingConfig) CalculateNetworkPartitionTimeout() (sleepDuration, waitTimeout time.Duration) {
	// 1. Disconnect grace period (if set, otherwise use default)
	gracePeriod := tc.DisconnectGracePeriod
	if gracePeriod == 0 {
		// Default grace period: max(3 * HeartbeatInterval, 5s)
		gracePeriod = 3 * tc.HeartbeatInterval
		if gracePeriod < 5*time.Second {
			gracePeriod = 5 * time.Second
		}
	}
	
	// 2. Key TTL expiration: Key expires TTL after last heartbeat
	// Worst case: Last heartbeat was just before disconnect
	ttlExpiration := tc.TTL
	
	// 3. Detection delays
	detectionDelays := tc.PeriodicCheckInterval + tc.InitialJitter + tc.MaxRetryBackoff
	
	// 4. Total minimum time
	totalMinimum := gracePeriod + ttlExpiration + detectionDelays
	
	// 5. Add buffer for NATS propagation
	sleepDuration = totalMinimum + tc.NATSPropagationBuffer
	
	// 6. Additional buffer for waitForLeader timeout
	waitTimeout = sleepDuration + 7*time.Second
	
	return sleepDuration, waitTimeout
}

// CalculateTTLExpirationTimeout calculates the timeout for TTL expiration scenarios
// This is used when a leader dies without graceful shutdown (no DeleteKey)
func (tc TimingConfig) CalculateTTLExpirationTimeout() (sleepDuration, waitTimeout time.Duration) {
	// 1. Disconnect grace period (default if not set)
	gracePeriod := tc.DisconnectGracePeriod
	if gracePeriod == 0 {
		gracePeriod = 3 * tc.HeartbeatInterval
		if gracePeriod < 5*time.Second {
			gracePeriod = 5 * time.Second
		}
	}
	
	// 2. Key TTL expiration
	ttlExpiration := tc.TTL
	
	// 3. Detection delays
	detectionDelays := tc.PeriodicCheckInterval + tc.InitialJitter + tc.MaxRetryBackoff
	
	// 4. Total minimum (grace period may be part of TTL wait)
	// Note: gracePeriod is considered in the overall timeout calculation
	_ = gracePeriod
	// In practice, key expires TTL after last heartbeat, so we use TTL + detection
	totalMinimum := ttlExpiration + detectionDelays
	
	// 5. Add buffer for NATS propagation
	sleepDuration = totalMinimum + tc.NATSPropagationBuffer
	
	// 6. Additional buffer for waitForLeader timeout
	waitTimeout = sleepDuration + 8*time.Second
	
	return sleepDuration, waitTimeout
}

// CalculateImmediateDeletionTimeout calculates the timeout for immediate key deletion scenarios
// This is used when DeleteKey=true (graceful shutdown)
func (tc TimingConfig) CalculateImmediateDeletionTimeout() (sleepDuration, waitTimeout time.Duration) {
	// 1. Key deletion: Immediate (DeleteKey=true)
	// 2. Detection delays
	detectionDelays := tc.PeriodicCheckInterval + tc.InitialJitter + tc.MaxRetryBackoff
	
	// 3. Total minimum
	totalMinimum := detectionDelays
	
	// 4. Add buffer for NATS propagation (smaller since deletion is immediate)
	sleepDuration = totalMinimum + 500*time.Millisecond
	
	// 5. Additional buffer for waitForLeader timeout
	waitTimeout = sleepDuration + 8*time.Second
	
	return sleepDuration, waitTimeout
}

