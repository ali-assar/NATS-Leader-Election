package leader

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

// TestConnectionMonitor_StatusTracking tests that connection monitor
// tracks status changes correctly. Note: This requires a real NATS connection
// to fully test, but we can test the status tracking logic.
func TestConnectionMonitor_StatusTracking(t *testing.T) {
	// Test status constants
	assert.Equal(t, ConnectionStatus(0), ConnectionStatusConnected)
	assert.Equal(t, ConnectionStatus(1), ConnectionStatusDisconnected)
	assert.Equal(t, ConnectionStatus(2), ConnectionStatusReconnected)
	assert.Equal(t, ConnectionStatus(3), ConnectionStatusClosed)

	// Test that status values are distinct
	statuses := []ConnectionStatus{
		ConnectionStatusConnected,
		ConnectionStatusDisconnected,
		ConnectionStatusReconnected,
		ConnectionStatusClosed,
	}

	// Verify all statuses are unique
	for i, s1 := range statuses {
		for j, s2 := range statuses {
			if i != j {
				assert.NotEqual(t, s1, s2, "Status values should be unique")
			}
		}
	}
}

// TestDisconnect_GracePeriod tests grace period behavior comprehensively
func TestDisconnect_GracePeriod(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     100 * time.Millisecond,
		DisconnectGracePeriod: 200 * time.Millisecond, // Fast for testing
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	kvElection, ok := election.(*kvElection)
	assert.True(t, ok)

	var demoteCalled bool
	election.OnDemote(func() {
		demoteCalled = true
	})

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Create disconnect handler manually for testing
	handler := &disconnectHandler{
		election: kvElection,
	}

	// Test 1: Grace period timer starts on disconnect
	handler.handleDisconnect()
	assert.NotNil(t, handler.timer, "Timer should be started on disconnect")

	// Test 2: Demotion after grace period expires
	// Wait for grace period + small buffer
	waitForCondition(t, func() bool {
		return !election.IsLeader()
	}, 500*time.Millisecond, "Leader should be demoted after grace period")

	assert.False(t, election.IsLeader(), "Should be demoted after grace period expires")
	assert.True(t, demoteCalled, "OnDemote should be called")

	defer election.Stop()
}

// TestDisconnect_NoDemotionIfReconnected tests that no demotion occurs
// if reconnected within grace period
func TestDisconnect_NoDemotionIfReconnected(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:                "leaders",
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     100 * time.Millisecond,
		DisconnectGracePeriod: 200 * time.Millisecond, // Faster for testing
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	kvElection, ok := election.(*kvElection)
	assert.True(t, ok)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)

	handler := &disconnectHandler{
		election: kvElection,
	}

	// Simulate disconnect
	handler.handleDisconnect()
	assert.NotNil(t, handler.timer, "Timer should be started")

	// Simulate reconnection before grace period expires
	// Stop the timer (simulating reconnect handler canceling it)
	handler.stop()
	assert.Nil(t, handler.timer, "Timer should be stopped on reconnect")

	// Wait a bit to ensure timer doesn't fire
	time.Sleep(100 * time.Millisecond)

	// Should still be leader (timer was stopped)
	assert.True(t, election.IsLeader(), "Should still be leader if reconnected within grace period")

	defer election.Stop()
}

// TestReconnect_Verification tests reconnection verification logic
func TestReconnect_Verification(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  100 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	kvElection, ok := election.(*kvElection)
	assert.True(t, ok)

	// Test verification with valid token (should pass)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Verify leadership - should succeed since we're the leader
	isValid, err := kvElection.validateToken(ctx)
	assert.NoError(t, err, "Token validation should succeed")
	assert.True(t, isValid, "Token should be valid")

	// Test verification failure scenario by invalidating token
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Update with different token to invalidate
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)

	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": "different-token-123",
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err)

	// Now verification should fail
	isValid, err = kvElection.validateToken(ctx)
	assert.False(t, isValid, "Token should be invalid after change")
	assert.Error(t, err, "Should return error for invalid token")

	defer election.Stop()
}
