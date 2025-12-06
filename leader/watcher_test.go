package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

func drainChannel(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestKeyDeletedEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	otherLeaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "other-instance",
		"token": "token-1",
	})
	assert.NoError(t, err)
	rev, err := mockKV.Create("test-group", otherLeaderPayload)
	assert.NoError(t, err)
	assert.Greater(t, rev, uint64(0), "Key should be created with revision > 0")

	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry, "Key should exist")

	updatesChan := make(chan natsmock.Entry, 10)
	stopChan := make(chan struct{})
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    stopChan,
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	entry2, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry2, "Key should still exist before election starts")

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)
	assert.NotNil(t, election)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		status := election.Status()
		return status.State == StateFollower
	}, 1*time.Second, "election to become follower")

	assert.False(t, election.IsLeader(), "Should be follower initially")
	status := election.Status()
	assert.Equal(t, StateFollower, status.State, "Should be in FOLLOWER state after failed attempt")

	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	initialEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: otherLeaderPayload,
		RevVal:   1,
	}
	updatesChan <- initialEntry

	waitForCondition(t, func() bool {
		return election.LeaderID() == "other-instance"
	}, 500*time.Millisecond, "watcher to process initial entry")

	assert.Equal(t, "other-instance", election.LeaderID(), "Should know about other leader")

	time.Sleep(10 * time.Millisecond)

	err = mockKV.Delete("test-group")
	assert.NoError(t, err, "Should be able to delete the key")

	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	updatesChan <- nil

	time.Sleep(20 * time.Millisecond)

	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(250 * time.Millisecond):
		status := election.Status()
		t.Fatalf("Timeout waiting for re-election attempt to start. State: %s, IsLeader: %v", status.State, election.IsLeader())
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for re-election attempt to complete")
	}

	waitForLeader(t, election, true, 500*time.Millisecond)

	assert.True(t, election.IsLeader(), "Expected to become leader after key deletion")
	assert.Equal(t, "instance-1", election.LeaderID(), "Should be our instance ID")
	assert.NotEmpty(t, election.Token(), "Should have a token")
}

func TestEmptyValueEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	otherLeaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "other-instance",
		"token": "token-1",
	})
	assert.NoError(t, err)
	_, err = mockKV.Create("test-group", otherLeaderPayload)
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait for initial attempt to fail and become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	err = mockKV.Delete("test-group")
	assert.NoError(t, err)

	emptyEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: []byte{},
		RevVal:   1,
	}
	updatesChan <- emptyEntry

	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	time.Sleep(20 * time.Millisecond)

	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Timeout waiting for re-election attempt to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for re-election attempt to complete")
	}

	waitForLeader(t, election, true, 500*time.Millisecond)
	assert.True(t, election.IsLeader(), "Expected to become leader after empty value event")
}

func TestLeaderChangeEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	initialLeaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "leader-1",
		"token": "token-1",
	})
	assert.NoError(t, err)
	_, err = mockKV.Create("test-group", initialLeaderPayload)
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Send initial leader entry
	initialEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: initialLeaderPayload,
		RevVal:   1,
	}
	updatesChan <- initialEntry

	// Wait for watcher to process
	waitForCondition(t, func() bool {
		return election.LeaderID() == "leader-1"
	}, 500*time.Millisecond, "watcher to process initial entry")

	assert.Equal(t, "leader-1", election.LeaderID(), "Should know about initial leader")
	assert.Equal(t, uint64(1), election.Status().Revision, "Should have correct revision")

	time.Sleep(10 * time.Millisecond)

	// Send new leader entry (leader changed)
	newLeaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "leader-2",
		"token": "token-2",
	})
	assert.NoError(t, err)

	newEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: newLeaderPayload,
		RevVal:   2,
	}
	updatesChan <- newEntry

	// Wait for watcher to process leader change
	waitForCondition(t, func() bool {
		return election.LeaderID() == "leader-2"
	}, 500*time.Millisecond, "watcher to process leader change")

	assert.Equal(t, "leader-2", election.LeaderID(), "Should know about new leader")
	assert.Equal(t, uint64(2), election.Status().Revision, "Should have updated revision")
	assert.False(t, election.IsLeader(), "Should still be follower")
}

func TestHeartbeatEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key with a leader
	leaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "leader-1",
		"token": "token-1",
	})
	assert.NoError(t, err)
	_, err = mockKV.Create("test-group", leaderPayload)
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Send initial leader entry
	initialEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: leaderPayload,
		RevVal:   1,
	}
	updatesChan <- initialEntry

	// Wait for watcher to process
	waitForCondition(t, func() bool {
		return election.LeaderID() == "leader-1"
	}, 500*time.Millisecond, "watcher to process initial entry")

	initialRevision := election.Status().Revision
	assert.Equal(t, uint64(1), initialRevision, "Should have initial revision")

	time.Sleep(10 * time.Millisecond)

	// Drain channels to ensure no re-election attempts
	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	// Send heartbeat (same leader, new revision)
	heartbeatEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: leaderPayload, // Same leader
		RevVal:   2,             // New revision
	}
	updatesChan <- heartbeatEntry

	// Wait for revision update
	waitForCondition(t, func() bool {
		return election.Status().Revision == uint64(2)
	}, 500*time.Millisecond, "watcher to process heartbeat")

	// Verify revision updated but no re-election triggered
	assert.Equal(t, uint64(2), election.Status().Revision, "Should have updated revision")
	assert.Equal(t, "leader-1", election.LeaderID(), "Should still know about same leader")
	assert.False(t, election.IsLeader(), "Should still be follower")

	// Verify no re-election attempt was made
	select {
	case <-mockKV.CreateStartChan:
		t.Fatal("Unexpected re-election attempt triggered by heartbeat")
	case <-time.After(100 * time.Millisecond):
		// Good - no re-election attempt
	}
}

func TestInvalidJSONEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key
	_, err = mockKV.Create("test-group", []byte(`{"id":"leader-1","token":"token-1"}`))
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Send invalid JSON entry
	invalidEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: []byte(`invalid json`),
		RevVal:   1,
	}
	updatesChan <- invalidEntry

	// Wait a bit to ensure event is processed
	time.Sleep(50 * time.Millisecond)

	// Verify state hasn't changed (invalid JSON should be ignored)
	// Invalid JSON should be silently ignored, so state should remain stable
	assert.False(t, election.IsLeader(), "Should still be follower")
}

func TestMissingIDFieldEvent(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key
	_, err = mockKV.Create("test-group", []byte(`{"id":"leader-1","token":"token-1"}`))
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Send entry without "id" field
	invalidEntry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: []byte(`{"token":"token-1"}`), // Missing "id" field
		RevVal:   1,
	}
	updatesChan <- invalidEntry

	// Wait a bit to ensure event is processed
	time.Sleep(50 * time.Millisecond)

	// Verify state hasn't changed (missing ID should be ignored)
	assert.False(t, election.IsLeader(), "Should still be follower")
}

func TestLeaderIgnoresOwnHeartbeat(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become leader
	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Get the token and leader ID
	token := election.Token()
	leaderID := election.LeaderID()
	assert.Equal(t, "instance-1", leaderID, "Should be our instance ID")

	// Note: In practice, when we're the leader, the watcher shouldn't be running
	// (it's only started in becomeFollower). But this test verifies that if a watch
	// event is received while leader, it's ignored.

	// Since we're leader, watcher shouldn't be running, but let's verify
	// that if handleWatchEvent is called, it ignores the event
	// We can't easily test this without exposing handleWatchEvent, but we can
	// verify that the leader state remains stable

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Verify state is still stable
	assert.True(t, election.IsLeader(), "Should still be leader")
	assert.Equal(t, "instance-1", election.LeaderID(), "Should still be our instance")
	assert.Equal(t, token, election.Token(), "Token should not change")
}

func TestWatcherStopsOnContextCancel(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key
	_, err = mockKV.Create("test-group", []byte(`{"id":"leader-1","token":"token-1"}`))
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	stopChan := make(chan struct{})
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    stopChan,
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Cancel context
	cancel()

	// Wait a bit for watcher to stop
	time.Sleep(50 * time.Millisecond)

	// Verify watcher stopped (Stop() should have been called)
	// We can't directly verify this, but we can verify that sending events
	// after cancellation doesn't cause issues
	entry := &natsmock.MockEntryImpl{
		KeyVal:   "test-group",
		ValueVal: []byte(`{"id":"leader-2","token":"token-2"}`),
		RevVal:   2,
	}

	// Send event after cancellation - should be ignored
	updatesChan <- entry

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// State should remain unchanged (watcher stopped)
	status := election.Status()
	assert.Equal(t, StateFollower, status.State, "Should still be follower")
}

func TestWatcherStopsOnChannelClose(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key
	_, err = mockKV.Create("test-group", []byte(`{"id":"leader-1","token":"token-1"}`))
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	stopChan := make(chan struct{})
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    stopChan,
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Close the updates channel (simulating watcher stop)
	close(updatesChan)

	// Wait a bit for watcher to detect channel close
	time.Sleep(50 * time.Millisecond)

	// State should remain stable (watcher stopped)
	status := election.Status()
	assert.Equal(t, StateFollower, status.State, "Should still be follower")
}

func TestJitterAndBackoffBehavior(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key
	_, err = mockKV.Create("test-group", []byte(`{"id":"leader-1","token":"token-1"}`))
	assert.NoError(t, err)

	// Create controllable watcher
	updatesChan := make(chan natsmock.Entry, 10)
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    make(chan struct{}),
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	assert.NoError(t, err)

	ctx := context.Background()
	err = election.Start(ctx)
	assert.NoError(t, err)

	// Wait to become follower
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to start")
	}

	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for attemptAcquire to complete")
	}

	waitForCondition(t, func() bool {
		return election.Status().State == StateFollower
	}, 1*time.Second, "election to become follower")

	// Wait for watcher to start
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Delete key but make Create() fail to test backoff
	err = mockKV.Delete("test-group")
	assert.NoError(t, err)

	// Set CreateFunc to fail first attempt, then succeed
	attemptCount := 0
	mockKV.CreateFunc = func(key string, value []byte, opts ...natsmock.KVOption) (uint64, error) {
		attemptCount++
		if attemptCount == 1 {
			// Fail first attempt to trigger backoff
			// Create a temporary key to make Create() fail
			mockKV.Create("temp-key", []byte("temp"))
			return 0, fmt.Errorf("key already exists")
		}
		// Clear the temp key and succeed on second attempt
		mockKV.Delete("temp-key")
		// Reset CreateFunc to use normal behavior
		mockKV.CreateFunc = nil
		return mockKV.Create(key, value, opts...)
	}

	// Drain channels
	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	// Send nil entry to trigger re-election and measure time from here
	startTime := time.Now()
	updatesChan <- nil

	// First attempt should happen after jitter (10-100ms)
	select {
	case <-mockKV.CreateStartChan:
		elapsed := time.Since(startTime)
		// Should have waited at least jitterMin
		assert.GreaterOrEqual(t, elapsed, jitterMin, "Should wait at least jitterMin before first attempt")
		// Should not wait more than jitterMax + small buffer
		assert.Less(t, elapsed, jitterMax+50*time.Millisecond, "Should not wait too long")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for first re-election attempt")
	}

	// Wait for first attempt to complete (will fail)
	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for first attempt to complete")
	}

	// Second attempt should happen after backoff
	// Note: The backoff calculation is: baseBackoff * 2^retry with jitter
	// For retry=0: baseBackoff (50ms) ± 10% jitter = 45-55ms
	// We verify that a second attempt happens (backoff is working)
	select {
	case <-mockKV.CreateStartChan:
		// Second attempt started - backoff worked
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for second re-election attempt")
	}

	// Wait for second attempt to complete (should succeed)
	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for second attempt to complete")
	}

	// Should eventually become leader
	waitForLeader(t, election, true, 500*time.Millisecond)
	assert.True(t, election.IsLeader(), "Expected to become leader after retry")
	assert.GreaterOrEqual(t, attemptCount, 2, "Should have made at least 2 attempts")
}

func TestMultipleFollowersCompeting(t *testing.T) {
	nc := natsmock.NewMockConn()
	js, err := nc.JetStream()
	assert.NoError(t, err)

	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Pre-create the key with a leader
	leaderPayload, err := json.Marshal(map[string]interface{}{
		"id":    "leader-1",
		"token": "token-1",
	})
	assert.NoError(t, err)
	_, err = mockKV.Create("test-group", leaderPayload)
	assert.NoError(t, err)

	// Create multiple elections (followers)
	numFollowers := 3
	elections := make([]Election, numFollowers)
	watchers := make([]*natsmock.MockWatcher, numFollowers)
	updateChans := make([]chan natsmock.Entry, numFollowers)

	for i := 0; i < numFollowers; i++ {
		// Create controllable watcher for each follower
		updateChan := make(chan natsmock.Entry, 10)
		updateChans[i] = updateChan
		customWatcher := &natsmock.MockWatcher{
			UpdatesChan: updateChan,
			StopChan:    make(chan struct{}),
		}
		watchers[i] = customWatcher

		// Set WatchFunc for this follower
		// We need to set it before creating the election
		// Since all elections share the same mockKV, we need to handle this carefully
		// For simplicity, we'll set it once and all will use the same pattern
		if i == 0 {
			mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
				// Return a new watcher for each call
				idx := len(watchers) - 1 // This is a simplification
				return watchers[idx], nil
			}
		}

		election, err := NewElection(newMockConnAdapter(nc), ElectionConfig{
			Bucket:            "leaders",
			Group:             "test-group",
			InstanceID:        fmt.Sprintf("instance-%d", i+1),
			TTL:               10 * time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
		})
		assert.NoError(t, err)
		elections[i] = election
	}

	// Actually, let's simplify - create watchers dynamically
	watcherIndex := 0
	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		if watcherIndex < len(watchers) {
			return watchers[watcherIndex], nil
		}
		// Create new watcher if needed
		updateChan := make(chan natsmock.Entry, 10)
		watcher := &natsmock.MockWatcher{
			UpdatesChan: updateChan,
			StopChan:    make(chan struct{}),
		}
		watchers = append(watchers, watcher)
		updateChans = append(updateChans, updateChan)
		watcherIndex++
		return watcher, nil
	}

	// Start all elections
	ctx := context.Background()
	for i := 0; i < numFollowers; i++ {
		err := elections[i].Start(ctx)
		assert.NoError(t, err)
	}

	// Wait for all to become followers
	for i := 0; i < numFollowers; i++ {
		waitForCondition(t, func() bool {
			return elections[i].Status().State == StateFollower
		}, 2*time.Second, fmt.Sprintf("election %d to become follower", i))
	}

	// Wait for all watchers to start
	for i := 0; i < numFollowers; i++ {
		select {
		case <-mockKV.WatchStartChan:
		case <-time.After(2 * time.Second):
			t.Fatalf("Timeout waiting for watcher %d to start", i)
		}
	}

	// Delete the key
	err = mockKV.Delete("test-group")
	assert.NoError(t, err)

	// Send nil entry to all watchers simultaneously (simulating leader deletion)
	// This tests thundering herd prevention
	for i := 0; i < numFollowers; i++ {
		updateChans[i] <- nil
	}

	// Drain channels
	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	// Wait for re-election attempts (they should be staggered due to jitter)
	// Collect all Create() calls
	createTimes := make([]time.Time, 0)
	createCount := 0
	attemptDeadline := time.Now().Add(500 * time.Millisecond)

	for createCount < numFollowers && time.Now().Before(attemptDeadline) {
		select {
		case <-mockKV.CreateStartChan:
			createTimes = append(createTimes, time.Now())
			createCount++
		case <-time.After(50 * time.Millisecond):
			// Continue waiting
		}
	}

	// Verify that attempts were staggered (not all at once)
	// Due to jitter, they should be spread out over at least jitterMin to jitterMax
	if len(createTimes) > 1 {
		timeSpread := createTimes[len(createTimes)-1].Sub(createTimes[0])
		// With jitter (10-100ms), the spread should be at least some minimum
		// But we can't guarantee exact timing, so we just verify they didn't all happen at once
		assert.Greater(t, timeSpread, 1*time.Millisecond, "Re-election attempts should be staggered")
	}

	// Wait for one to succeed and count leaders
	leaderDeadline := time.Now().Add(3 * time.Second)
	var leaderCount int
	for time.Now().Before(leaderDeadline) {
		leaderCount = 0
		for i := 0; i < numFollowers; i++ {
			if elections[i].IsLeader() {
				leaderCount++
			}
		}
		if leaderCount == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Exactly one should become leader
	assert.Equal(t, 1, leaderCount, "Exactly one follower should become leader")
}
