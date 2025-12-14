package leader

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

// Test helpers are now in test_helpers.go

// TestHeartbeatLoop_Success verifies that the heartbeat loop successfully updates
// the leadership key periodically, keeping the TTL alive and maintaining leadership.
func TestHeartbeatLoop_Success(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	nc := natsmock.NewMockConn()
	election, err := NewElection(NewMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and become leader
	WaitForLeader(t, election, true, 1*time.Second)

	// Verify we became leader
	assert.True(t, election.IsLeader(), "Expected to be leader")
	assert.NotEmpty(t, election.Token(), "Expected to have a token")
	assert.NotEmpty(t, election.LeaderID(), "Expected to have leader ID")

	// Capture initial state before heartbeat
	initialStatus := election.Status()
	initialRev := initialStatus.Revision
	initialHeartbeat := initialStatus.LastHeartbeat

	// Wait for heartbeat to occur (wait up to 2x interval)
	WaitForHeartbeat(t, election, initialHeartbeat, heartbeatInterval*2)

	// Verify heartbeat occurred successfully
	newStatus := election.Status()

	// Revision should have increased (Update() was called successfully)
	assert.Greater(t, newStatus.Revision, initialRev,
		"Expected revision to increase after heartbeat")

	// LastHeartbeat should be more recent
	assert.True(t, newStatus.LastHeartbeat.After(initialHeartbeat),
		"Expected lastHeartbeat to be updated after heartbeat")

	// Should still be leader (no demotion)
	assert.True(t, election.IsLeader(), "Expected to still be leader after successful heartbeat")
	assert.True(t, newStatus.IsLeader, "Status should show we're still leader")

	// Token and LeaderID should remain unchanged (same leadership session)
	assert.Equal(t, initialStatus.Token, newStatus.Token, "Token should not change")
	assert.Equal(t, initialStatus.LeaderID, newStatus.LeaderID, "LeaderID should not change")

	// Clean up
	err = election.Stop()
	assert.NoError(t, err)
}

// TestRevisionMismatch verifies that when a revision mismatch occurs (someone else
// updated the key), the leader detects the conflict and demotes itself immediately.
// This tests the conditional update mechanism and conflict detection.
func TestRevisionMismatch(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	nc := natsmock.NewMockConn()
	election, err := NewElection(NewMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	// Track if onDemote callback was called
	var demoteMu sync.Mutex
	demoteCalled := false
	election.OnDemote(func() {
		demoteMu.Lock()
		defer demoteMu.Unlock()
		demoteCalled = true
	})

	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and become leader
	WaitForLeader(t, election, true, 1*time.Second)

	// Verify we became leader
	assert.True(t, election.IsLeader(), "Expected to be leader")
	initialStatus := election.Status()
	initialRev := initialStatus.Revision

	// Get the SAME KV store that the election is using (from the same connection)
	js, err := nc.JetStream()
	assert.NoError(t, err)
	kv, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Simulate another leader updating the key (changes the revision)
	// This simulates what happens when someone else becomes leader or updates the key
	// The revision will change, causing our next heartbeat Update() to fail
	otherPayload := []byte(`{"id":"instance-2","token":"different-token"}`)
	_, err = kv.Update("test-group", otherPayload, initialRev)
	assert.NoError(t, err, "Should successfully update to simulate revision change")

	// Wait for heartbeat to fire - it will try to update with old revision
	// and should fail with "revision mismatch"
	WaitForLeader(t, election, false, heartbeatInterval*2)

	// Verify demotion happened (revision mismatch is a permanent error)
	assert.False(t, election.IsLeader(), "Expected to be demoted after revision mismatch")

	// Verify onDemote callback was called
	demoteMu.Lock()
	wasCalled := demoteCalled
	demoteMu.Unlock()
	assert.True(t, wasCalled, "Expected onDemote callback to be called")

	// Verify state changed to FOLLOWER
	finalStatus := election.Status()
	assert.Equal(t, StateFollower, finalStatus.State, "Expected state to be FOLLOWER")
	assert.False(t, finalStatus.IsLeader, "Status should show we're not leader")

	// Clean up
	err = election.Stop()
	assert.NoError(t, err)
}

// TestTransientError verifies that transient errors (like timeouts) don't cause
// immediate demotion. The heartbeat should retry on the next interval and recover.
// This tests the error classification and retry logic.
func TestTransientError(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	nc := natsmock.NewMockConn()
	election, err := NewElection(NewMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and become leader
	WaitForLeader(t, election, true, 1*time.Second)

	// Verify we became leader
	assert.True(t, election.IsLeader(), "Expected to be leader")

	// Capture initial state before failures
	initialStatus := election.Status()
	initialRev := initialStatus.Revision
	initialHeartbeat := initialStatus.LastHeartbeat

	// Get the MockKeyValue to control Update() behavior
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Set UpdateFunc to fail first time with transient error, succeed second time
	// This simulates a temporary network issue that resolves
	var callMu sync.Mutex
	callCount := 0
	mockKV.SetUpdateFunc(func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		callMu.Lock()
		callCount++
		count := callCount
		callMu.Unlock()
		if count == 1 {
			// First call fails with transient error (timeout)
			return 0, errors.New("timeout")
		}
		// Second call succeeds - clear the func to use normal Update logic
		mockKV.SetUpdateFunc(nil)
		return mockKV.Update(key, value, rev)
	})

	// Wait for first heartbeat - it will fail with transient error
	// We wait a bit longer than heartbeat interval to ensure it fired
	time.Sleep(heartbeatInterval + 20*time.Millisecond)

	// Verify after transient error: no demotion, state unchanged
	assert.True(t, election.IsLeader(), "Expected to still be leader after transient error")

	statusAfterFailure := election.Status()
	// Revision did NOT increase (Update failed)
	assert.Equal(t, initialRev, statusAfterFailure.Revision,
		"Expected revision to remain unchanged after transient error")

	// LastHeartbeat did NOT update (heartbeat failed)
	assert.Equal(t, initialHeartbeat, statusAfterFailure.LastHeartbeat,
		"Expected lastHeartbeat to remain unchanged after transient error")

	// Wait for second heartbeat - it should succeed now (UpdateFunc cleared)
	WaitForHeartbeat(t, election, initialHeartbeat, heartbeatInterval*2)

	// Verify after recovery: still leader, state updated
	assert.True(t, election.IsLeader(), "Expected to still be leader after recovery")

	statusAfterRecovery := election.Status()
	assert.Greater(t, statusAfterRecovery.Revision, initialRev,
		"Expected revision to increase after successful heartbeat")

	assert.True(t, statusAfterRecovery.LastHeartbeat.After(initialHeartbeat),
		"Expected lastHeartbeat to be updated after successful heartbeat")

	assert.Equal(t, initialStatus.Token, statusAfterRecovery.Token,
		"Token should not change")

	assert.Equal(t, initialStatus.LeaderID, statusAfterRecovery.LeaderID,
		"LeaderID should not change")

	err = election.Stop()
	assert.NoError(t, err)
}

// TestMultipleErrors verifies that after maxFailures (3) consecutive transient errors,
// the leader demotes itself and calls the onDemote callback.
// This tests the failure counting mechanism and threshold-based demotion.
func TestMultipleErrors(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	nc := natsmock.NewMockConn()
	election, err := NewElection(NewMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	// Set up onDemote callback to track when demotion occurs
	var demoteMu sync.Mutex
	demoteCalled := false
	election.OnDemote(func() {
		demoteMu.Lock()
		defer demoteMu.Unlock()
		demoteCalled = true
	})

	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and become leader
	WaitForLeader(t, election, true, 1*time.Second)

	// Verify we became leader
	assert.True(t, election.IsLeader(), "Expected to be leader")
	initialStatus := election.Status()
	assert.Equal(t, StateLeader, initialStatus.State, "Expected state to be LEADER")
	assert.True(t, initialStatus.IsLeader, "Status should show we're leader")

	// Get the MockKeyValue to control Update() behavior
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Set UpdateFunc to always fail with transient error (timeout)
	// This simulates network issues or temporary NATS unavailability
	// After 3 consecutive failures, the leader should demote
	mockKV.SetUpdateFunc(func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		return 0, errors.New("timeout")
	})

	// Wait for 3 heartbeats (each will fail) - wait for demotion
	WaitForLeader(t, election, false, heartbeatInterval*4)

	// Verify demotion occurred after maxFailures threshold
	assert.False(t, election.IsLeader(), "Expected to be demoted after 3 consecutive failures")
	demoteMu.Lock()
	wasCalled := demoteCalled
	demoteMu.Unlock()
	assert.True(t, wasCalled, "Expected onDemote callback to be called")

	// Verify state transition to FOLLOWER
	finalStatus := election.Status()
	assert.Equal(t, StateFollower, finalStatus.State, "Expected state to be FOLLOWER")
	assert.False(t, finalStatus.IsLeader, "Status should show we're not leader")

	// Verify revision didn't increase (no successful updates occurred)
	assert.Equal(t, initialStatus.Revision, finalStatus.Revision,
		"Expected revision to remain unchanged after all failures")

	err = election.Stop()
	assert.NoError(t, err)
}

// TestTimeoutError verifies that when Update() takes longer than the timeout duration,
// the heartbeat loop correctly times out and treats it as a transient error.
// This tests the timeout mechanism and ensures slow operations don't block indefinitely.
func TestTimeoutError(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	nc := natsmock.NewMockConn()
	election, err := NewElection(NewMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and become leader
	WaitForLeader(t, election, true, 1*time.Second)

	// Verify we became leader
	assert.True(t, election.IsLeader(), "Expected to be leader")

	// Get the MockKeyValue to control Update() behavior
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Capture initial state before timeout
	initialStatus := election.Status()
	initialRev := initialStatus.Revision
	initialHeartbeat := initialStatus.LastHeartbeat

	// Calculate expected timeout duration (min 1s, or half heartbeat)
	expectedTimeout := heartbeatInterval / 2
	if expectedTimeout < 1*time.Second {
		expectedTimeout = 1 * time.Second
	}

	// Use channels to control Update() timing precisely
	updateStartChan := make(chan struct{}, 1)
	updateCompleteChan := make(chan struct{}, 1)
	var callMu sync.Mutex
	callCount := 0

	// Set UpdateFunc to block on channel for first call, succeed for second
	mockKV.SetUpdateFunc(func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		callMu.Lock()
		callCount++
		count := callCount
		callMu.Unlock()
		if count == 1 {
			// First call: signal start, then block waiting for test to signal completion
			select {
			case updateStartChan <- struct{}{}:
			default:
			}
			// Block until test signals to complete (or timeout for safety)
			select {
			case <-updateCompleteChan:
				// Test signaled to complete
			case <-time.After(10 * time.Second):
				// Safety timeout
			}
			return rev + 1, nil
		}
		// Second call succeeds immediately (for recovery test)
		mockKV.SetUpdateFunc(nil)
		return mockKV.Update(key, value, rev)
	})

	// Wait for Update() to start (first heartbeat)
	select {
	case <-updateStartChan:
		// Update() started
	case <-time.After(heartbeatInterval * 2):
		t.Fatal("Timeout waiting for Update() to start")
	}

	// Wait for timeout to occur (heartbeat should timeout waiting for Update())
	time.Sleep(expectedTimeout + 100*time.Millisecond)

	// Verify timeout occurred and was treated as transient error
	// Should still be leader (timeout is transient, not permanent)
	assert.True(t, election.IsLeader(), "Expected to still be leader after timeout (transient error)")

	// Now signal Update() to complete (it was blocked)
	select {
	case updateCompleteChan <- struct{}{}:
	default:
	}

	// Wait for next heartbeat - it should succeed now (UpdateFunc cleared)
	WaitForHeartbeat(t, election, initialHeartbeat, heartbeatInterval*2)

	// Verify recovery: next heartbeat should succeed
	statusAfterRecovery := election.Status()
	assert.True(t, election.IsLeader(), "Expected to still be leader after recovery")

	// Revision should have increased (successful heartbeat after timeout)
	assert.Greater(t, statusAfterRecovery.Revision, initialRev,
		"Expected revision to increase after successful heartbeat recovery")

	// LastHeartbeat should be updated
	assert.True(t, statusAfterRecovery.LastHeartbeat.After(initialHeartbeat),
		"Expected lastHeartbeat to be updated after successful heartbeat recovery")

	err = election.Stop()
	assert.NoError(t, err)
}
