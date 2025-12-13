package leader

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPriorityTakeover_Basic tests basic priority takeover functionality
func TestPriorityTakeover_Basic(t *testing.T) {
	nc := natsmock.NewMockConn()

	// Create two elections with different priorities
	cfg1 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              10, // Lower priority
		AllowPriorityTakeover: true,
	}

	cfg2 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-2",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              20, // Higher priority
		AllowPriorityTakeover: true,
	}

	election1, err := NewElection(newMockConnAdapter(nc), cfg1)
	require.NoError(t, err)

	election2, err := NewElection(newMockConnAdapter(nc), cfg2)
	require.NoError(t, err)

	// Track callbacks
	var promote1Count, demote1Count int
	var promote2Count int
	var mu sync.Mutex

	election1.OnPromote(func(ctx context.Context, token string) {
		mu.Lock()
		defer mu.Unlock()
		promote1Count++
	})

	election1.OnDemote(func() {
		mu.Lock()
		defer mu.Unlock()
		demote1Count++
	})

	election2.OnPromote(func(ctx context.Context, token string) {
		mu.Lock()
		defer mu.Unlock()
		promote2Count++
	})

	// Start election1 first (lower priority)
	ctx := context.Background()
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	mu.Lock()
	assert.Equal(t, 1, promote1Count, "Election1's OnPromote should be called")
	mu.Unlock()

	// Start election2 (higher priority)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Wait a bit for election2 to start and attempt acquisition
	time.Sleep(100 * time.Millisecond)

	// Wait for election2 to take over
	// Priority takeover should happen quickly since election2 will attempt on Start()
	waitForLeader(t, election2, true, 3*time.Second)
	assert.True(t, election2.IsLeader(), "Election2 should take over leadership")

	// Wait a bit more for election1 to detect the takeover
	time.Sleep(200 * time.Millisecond)
	assert.False(t, election1.IsLeader(), "Election1 should be demoted")

	mu.Lock()
	assert.Equal(t, 1, promote2Count, "Election2's OnPromote should be called")
	assert.Equal(t, 1, demote1Count, "Election1's OnDemote should be called")
	mu.Unlock()
}

// TestPriorityTakeover_WithoutTakeoverEnabled tests that priority is ignored when AllowPriorityTakeover is false
func TestPriorityTakeover_WithoutTakeoverEnabled(t *testing.T) {
	nc := natsmock.NewMockConn()

	cfg1 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              10,    // Lower priority
		AllowPriorityTakeover: false, // Disabled
	}

	cfg2 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-2",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              20,    // Higher priority
		AllowPriorityTakeover: false, // Disabled
	}

	election1, err := NewElection(newMockConnAdapter(nc), cfg1)
	require.NoError(t, err)

	election2, err := NewElection(newMockConnAdapter(nc), cfg2)
	require.NoError(t, err)

	// Start election1 first
	ctx := context.Background()
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	// Start election2 (higher priority but takeover disabled)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Wait a bit to ensure no takeover happens
	time.Sleep(1 * time.Second)

	// Election1 should still be leader (first-come-first-served)
	assert.True(t, election1.IsLeader(), "Election1 should remain leader (takeover disabled)")
	assert.False(t, election2.IsLeader(), "Election2 should not take over (takeover disabled)")
}

// TestPriorityTakeover_EqualPriority tests that equal priority doesn't trigger takeover
func TestPriorityTakeover_EqualPriority(t *testing.T) {
	nc := natsmock.NewMockConn()

	cfg1 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              20,
		AllowPriorityTakeover: true,
	}

	cfg2 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-2",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              20, // Same priority
		AllowPriorityTakeover: true,
	}

	election1, err := NewElection(newMockConnAdapter(nc), cfg1)
	require.NoError(t, err)

	election2, err := NewElection(newMockConnAdapter(nc), cfg2)
	require.NoError(t, err)

	// Start election1 first
	ctx := context.Background()
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	// Start election2 (same priority)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Wait a bit
	time.Sleep(1 * time.Second)

	// Election1 should still be leader (equal priority, no takeover)
	assert.True(t, election1.IsLeader(), "Election1 should remain leader (equal priority)")
	assert.False(t, election2.IsLeader(), "Election2 should not take over (equal priority)")
}

// TestPriorityTakeover_LowerPriorityCannotTakeover tests that lower priority cannot take over
func TestPriorityTakeover_LowerPriorityCannotTakeover(t *testing.T) {
	nc := natsmock.NewMockConn()

	cfg1 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              20, // Higher priority
		AllowPriorityTakeover: true,
	}

	cfg2 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-2",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              10, // Lower priority
		AllowPriorityTakeover: true,
	}

	election1, err := NewElection(newMockConnAdapter(nc), cfg1)
	require.NoError(t, err)

	election2, err := NewElection(newMockConnAdapter(nc), cfg2)
	require.NoError(t, err)

	// Start election1 first (higher priority)
	ctx := context.Background()
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	// Start election2 (lower priority)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Wait a bit
	time.Sleep(1 * time.Second)

	// Election1 should still be leader (lower priority cannot take over)
	assert.True(t, election1.IsLeader(), "Election1 should remain leader (higher priority)")
	assert.False(t, election2.IsLeader(), "Election2 should not take over (lower priority)")
}

// TestPriorityTakeover_Validation tests that validation rejects invalid priority configuration
func TestPriorityTakeover_Validation(t *testing.T) {
	nc := natsmock.NewMockConn()

	// Test: Priority <= 0 when AllowPriorityTakeover is true
	cfg := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              0, // Invalid: must be > 0
		AllowPriorityTakeover: true,
	}

	_, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.Error(t, err, "Should reject priority 0 when AllowPriorityTakeover is true")
	assert.Contains(t, err.Error(), "priority must be > 0", "Error should mention priority validation")

	// Test: Negative priority
	cfg.Priority = -1
	_, err = NewElection(newMockConnAdapter(nc), cfg)
	assert.Error(t, err, "Should reject negative priority")

	// Test: Valid configuration (priority > 0 with takeover enabled)
	cfg.Priority = 10
	_, err = NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Should accept valid priority configuration")

	// Test: Priority 0 with takeover disabled (should be OK)
	cfg.Priority = 0
	cfg.AllowPriorityTakeover = false
	_, err = NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Should accept priority 0 when takeover is disabled")
}

// TestPriorityTakeover_MultipleHighPriority tests multiple high-priority instances competing
func TestPriorityTakeover_MultipleHighPriority(t *testing.T) {
	nc := natsmock.NewMockConn()

	// Create low priority leader
	cfg1 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              10, // Low priority
		AllowPriorityTakeover: true,
	}

	// Create two high priority instances
	cfg2 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-2",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              30, // High priority
		AllowPriorityTakeover: true,
	}

	cfg3 := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-3",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              30, // Same high priority
		AllowPriorityTakeover: true,
	}

	election1, err := NewElection(newMockConnAdapter(nc), cfg1)
	require.NoError(t, err)

	election2, err := NewElection(newMockConnAdapter(nc), cfg2)
	require.NoError(t, err)

	election3, err := NewElection(newMockConnAdapter(nc), cfg3)
	require.NoError(t, err)

	// Start election1 first (low priority)
	ctx := context.Background()
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	// Start both high priority instances simultaneously
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	err = election3.Start(ctx)
	require.NoError(t, err)
	defer election3.Stop()

	// Wait for one of them to take over
	waitForCondition(t, func() bool {
		return election2.IsLeader() || election3.IsLeader()
	}, 3*time.Second, "one high-priority instance to take over")

	// Wait a bit more for system to stabilize
	time.Sleep(500 * time.Millisecond)

	// Verify only one is leader
	leaderCount := 0
	if election1.IsLeader() {
		leaderCount++
	}
	if election2.IsLeader() {
		leaderCount++
	}
	if election3.IsLeader() {
		leaderCount++
	}
	assert.Equal(t, 1, leaderCount, "Only one instance should be leader")
	assert.False(t, election1.IsLeader(), "Low priority instance should not be leader")
}

// TestPriorityTakeover_HeartbeatIncludesPriority tests that heartbeat includes priority
func TestPriorityTakeover_HeartbeatIncludesPriority(t *testing.T) {
	nc := natsmock.NewMockConn()

	cfg := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		Priority:              25,
		AllowPriorityTakeover: true,
	}

	election, err := NewElection(newMockConnAdapter(nc), cfg)
	require.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	require.NoError(t, err)
	defer election.Stop()

	// Wait to become leader
	waitForLeader(t, election, true, 2*time.Second)

	// Wait for at least one heartbeat
	time.Sleep(300 * time.Millisecond)

	// Get the key from KV store and verify priority is in payload
	adapter := newMockConnAdapter(nc)
	js, err := adapter.JetStream()
	require.NoError(t, err)

	kv, err := js.KeyValue("leaders")
	require.NoError(t, err)

	entry, err := kv.Get("test-group")
	require.NoError(t, err)
	require.NotNil(t, entry)

	var payload leadershipPayload
	err = json.Unmarshal(entry.Value(), &payload)
	require.NoError(t, err)

	assert.Equal(t, 25, payload.Priority, "Payload should include priority")
	assert.Equal(t, "instance-1", payload.ID, "Payload should include instance ID")
	assert.NotEmpty(t, payload.Token, "Payload should include token")
}
