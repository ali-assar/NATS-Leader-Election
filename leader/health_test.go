package leader

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

// mockHealthChecker is a mock implementation of HealthChecker for testing
type mockHealthChecker struct {
	healthy atomic.Bool
	block   chan struct{}
	calls   atomic.Int32
}

func newMockHealthChecker(healthy bool) *mockHealthChecker {
	m := &mockHealthChecker{}
	m.healthy.Store(healthy)
	return m
}

func (m *mockHealthChecker) Check(ctx context.Context) bool {
	m.calls.Add(1)
	if m.block != nil {
		select {
		case <-ctx.Done():
			return false
		case <-m.block:
		}
	}
	return m.healthy.Load()
}

func (m *mockHealthChecker) setHealthy(healthy bool) {
	m.healthy.Store(healthy)
}

func (m *mockHealthChecker) getCalls() int {
	return int(m.calls.Load())
}

func TestHealthCheck_HealthyLeaderContinues(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	healthChecker := newMockHealthChecker(true)
	cfg.HealthChecker = healthChecker
	cfg.MaxConsecutiveFailures = 3

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for multiple heartbeat cycles
	time.Sleep(600 * time.Millisecond)

	// Should still be leader (health check passes)
	assert.True(t, election.IsLeader(), "Should still be leader after health checks")

	// Verify health checker was called
	assert.Greater(t, healthChecker.getCalls(), 0, "Health checker should be called")

	defer election.Stop()
}

func TestHealthCheck_UnhealthyLeaderDemotes(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	healthChecker := newMockHealthChecker(false)
	cfg.HealthChecker = healthChecker
	cfg.MaxConsecutiveFailures = 3

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	var demoteCalled bool
	election.OnDemote(func() {
		demoteCalled = true
	})

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for health checks to run (3+ heartbeat intervals)
	// With 200ms heartbeat and 3 failures needed, wait at least 600ms
	waitForLeader(t, election, false, 1*time.Second)

	// Should be demoted
	assert.False(t, election.IsLeader(), "Should be demoted after health check failures")

	// Wait for onDemote callback
	waitForCondition(t, func() bool {
		return demoteCalled
	}, 500*time.Millisecond, "OnDemote callback")

	assert.True(t, demoteCalled, "OnDemote callback should be called")
	assert.GreaterOrEqual(t, healthChecker.getCalls(), 3, "Health checker should be called at least 3 times")

	defer election.Stop()
}

func TestHealthCheck_IntermittentFailures(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	healthChecker := newMockHealthChecker(true)
	cfg.HealthChecker = healthChecker
	cfg.MaxConsecutiveFailures = 3

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Simulate intermittent failures
	// Fail twice, then succeed (counter should reset)
	healthChecker.setHealthy(false)
	time.Sleep(400 * time.Millisecond) // 2 heartbeat cycles

	// Should still be leader (only 2 failures, threshold is 3)
	assert.True(t, election.IsLeader(), "Should still be leader after 2 failures")

	// Now succeed (counter should reset)
	healthChecker.setHealthy(true)
	time.Sleep(300 * time.Millisecond) // 1-2 heartbeat cycles

	// Should still be leader
	assert.True(t, election.IsLeader(), "Should still be leader after recovery")

	// Fail again (counter should start from 0)
	healthChecker.setHealthy(false)
	time.Sleep(400 * time.Millisecond) // 2 heartbeat cycles

	// Should still be leader (only 2 failures again)
	assert.True(t, election.IsLeader(), "Should still be leader after intermittent failures")

	defer election.Stop()
}

func TestHealthCheck_Timeout(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	healthChecker := &mockHealthChecker{
		healthy: atomic.Bool{},
		block:   make(chan struct{}), // Block forever
	}
	healthChecker.healthy.Store(true)
	cfg.HealthChecker = healthChecker
	cfg.MaxConsecutiveFailures = 3

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for health checks to run (should timeout after 100ms)
	// With 3 failures needed and timeout treated as unhealthy, wait at least 600ms
	waitForLeader(t, election, false, 1*time.Second)

	// Should be demoted (timeout is treated as unhealthy)
	assert.False(t, election.IsLeader(), "Should be demoted after health check timeouts")

	defer election.Stop()
}

func TestHealthCheck_NoHealthChecker(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	// No health checker configured
	cfg.HealthChecker = nil

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for multiple heartbeat cycles
	time.Sleep(600 * time.Millisecond)

	// Should still be leader (no health checking)
	assert.True(t, election.IsLeader(), "Should still be leader without health checker")

	defer election.Stop()
}

func TestHealthCheck_CustomThreshold(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	healthChecker := newMockHealthChecker(false)
	cfg.HealthChecker = healthChecker
	cfg.MaxConsecutiveFailures = 5 // Custom threshold

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for 4 heartbeat cycles (should not demote yet)
	time.Sleep(800 * time.Millisecond)

	// Should still be leader (only 4 failures, threshold is 5)
	assert.True(t, election.IsLeader(), "Should still be leader after 4 failures")

	// Wait for one more cycle (5th failure)
	time.Sleep(300 * time.Millisecond)

	// Should be demoted now
	waitForLeader(t, election, false, 500*time.Millisecond)
	assert.False(t, election.IsLeader(), "Should be demoted after 5 failures")

	defer election.Stop()
}
