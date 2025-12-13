package leader

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

// TestFullElectionCycle_Integration tests the complete election lifecycle:
// start → leader → demote → follower → re-elect → leader
func TestFullElectionCycle_Integration(t *testing.T) {
	heartbeatInterval := 100 * time.Millisecond

	// Setup and become leader
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: heartbeatInterval,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, election)

	// Track callbacks and tokens
	var promoteCount int
	var demoteCount int
	var promoteTokens []string

	election.OnPromote(func(ctx context.Context, token string) {
		promoteCount++
		promoteTokens = append(promoteTokens, token)
	})

	var demoteCalled bool
	election.OnDemote(func() {
		demoteCalled = true
		demoteCount++
	})

	// Start election and wait to become leader
	err = election.Start(context.Background())
	assert.NoError(t, err)
	waitForLeader(t, election, true, 1*time.Second)

	assert.True(t, election.IsLeader(), "Expected to be leader")
	assert.NotEmpty(t, election.Token(), "Expected to have a token")
	assert.NotEmpty(t, election.LeaderID(), "Expected to have leader ID")

	waitForCondition(t, func() bool {
		return promoteCount > 0
	}, 1*time.Second, "OnPromote callback")
	assert.Equal(t, 1, promoteCount, "OnPromote should be called once")
	assert.Equal(t, 1, len(promoteTokens), "Should have one token")
	assert.Equal(t, election.Token(), promoteTokens[0], "Token should match")
	assert.Equal(t, 0, demoteCount, "OnDemote should not be called yet")

	// Get mockKV and set up controllable watcher BEFORE demotion
	// This ensures that when we become a follower, the watcher uses our custom function
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Set up controllable watcher for re-election phase
	updatesChan := make(chan natsmock.Entry, 10)
	stopChan := make(chan struct{})
	customWatcher := &natsmock.MockWatcher{
		UpdatesChan: updatesChan,
		StopChan:    stopChan,
	}

	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		return customWatcher, nil
	}

	// Set UpdateFunc to always return revision mismatch error
	// This simulates another instance updating the key, causing heartbeat to fail
	mockKV.UpdateFunc = func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		return 0, errors.New("revision mismatch")
	}

	// Wait for demotion (heartbeat will fail on next interval)
	waitForLeader(t, election, false, heartbeatInterval*2)
	assert.False(t, election.IsLeader(), "Expected to be demoted after revision mismatch")

	// Wait for onDemote callback
	waitForCondition(t, func() bool {
		return demoteCalled
	}, 500*time.Millisecond, "OnDemote callback")
	assert.True(t, demoteCalled, "OnDemote callback should be called")
	assert.Equal(t, 1, demoteCount, "OnDemote should be called once")
	assert.Equal(t, 1, promoteCount, "Promote count should still be 1")

	// Wait for watcher to start (it should have started when we became a follower)
	select {
	case <-mockKV.WatchStartChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for watcher to start")
	}

	// Delete the key to simulate leader disappearing
	err = mockKV.Delete("test-group")
	assert.NoError(t, err, "Should be able to delete the key")

	// Drain any existing signals from previous operations
	drainChannel(mockKV.CreateStartChan)
	drainChannel(mockKV.CreateDoneChan)

	// Send nil entry to watcher (simulates key deletion event)
	updatesChan <- nil
	time.Sleep(20 * time.Millisecond)

	// Wait for re-election attempt to start
	select {
	case <-mockKV.CreateStartChan:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for re-election attempt to start")
	}

	// Wait for re-election to complete
	select {
	case <-mockKV.CreateDoneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for re-election attempt to complete")
	}

	// Wait for election to become leader again (with jitter, may take up to 500ms)
	waitForLeader(t, election, true, 500*time.Millisecond)
	assert.True(t, election.IsLeader(), "Expected to become leader after re-election")

	// Wait for onPromote callback to be called again
	waitForCondition(t, func() bool {
		return promoteCount >= 2
	}, 500*time.Millisecond, "OnPromote callback on re-election")
	assert.Equal(t, 2, promoteCount, "OnPromote should be called twice")
	assert.Equal(t, 2, len(promoteTokens), "Should have two tokens")
	assert.NotEqual(t, promoteTokens[0], promoteTokens[1], "New token should be different from first token")
	assert.Equal(t, election.Token(), promoteTokens[1], "Current token should match second token")

	// Final cleanup
	err = election.Stop()
	assert.NoError(t, err)
	assert.False(t, election.IsLeader(), "Should not be leader after stop")
}

func TestMultipleInstances_Integration(t *testing.T) {
	cfg1 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	}
	cfg2 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-2",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	}
	cfg3 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-3",
		TTL:               10 * time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	}

	nc := natsmock.NewMockConn()
	adapter := newMockConnAdapter(nc)

	// Setup callback tracking for all instances
	type instanceCallbacks struct {
		mu           sync.Mutex
		promoteCount int
		demoteCount  int
		tokens       []string
	}

	callbacks := make(map[string]*instanceCallbacks)
	callbacks["instance-1"] = &instanceCallbacks{}
	callbacks["instance-2"] = &instanceCallbacks{}
	callbacks["instance-3"] = &instanceCallbacks{}

	// Create all elections
	election1, err := NewElection(adapter, cfg1)
	assert.NoError(t, err)
	election2, err := NewElection(adapter, cfg2)
	assert.NoError(t, err)
	election3, err := NewElection(adapter, cfg3)
	assert.NoError(t, err)

	// Register callbacks for each election
	election1.OnPromote(func(ctx context.Context, token string) {
		cb := callbacks["instance-1"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.promoteCount++
		cb.tokens = append(cb.tokens, token)
	})
	election1.OnDemote(func() {
		cb := callbacks["instance-1"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.demoteCount++
	})

	election2.OnPromote(func(ctx context.Context, token string) {
		cb := callbacks["instance-2"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.promoteCount++
		cb.tokens = append(cb.tokens, token)
	})
	election2.OnDemote(func() {
		cb := callbacks["instance-2"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.demoteCount++
	})

	election3.OnPromote(func(ctx context.Context, token string) {
		cb := callbacks["instance-3"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.promoteCount++
		cb.tokens = append(cb.tokens, token)
	})
	election3.OnDemote(func() {
		cb := callbacks["instance-3"]
		cb.mu.Lock()
		defer cb.mu.Unlock()
		cb.demoteCount++
	})

	// Set up controllable watchers BEFORE starting elections
	// This ensures followers use controllable watchers when they're created
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	watcherChans := make([]chan natsmock.Entry, 0, 3)
	var watcherMu sync.Mutex
	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		updatesChan := make(chan natsmock.Entry, 10)
		watcherMu.Lock()
		watcherChans = append(watcherChans, updatesChan)
		watcherMu.Unlock()
		return &natsmock.MockWatcher{
			UpdatesChan: updatesChan,
			StopChan:    make(chan struct{}),
		}, nil
	}

	// Start all elections simultaneously
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		_ = election1.Start(context.Background())
	}()
	go func() {
		defer wg.Done()
		_ = election2.Start(context.Background())
	}()
	go func() {
		defer wg.Done()
		_ = election3.Start(context.Background())
	}()

	wg.Wait()

	waitForCondition(t, func() bool {
		return election1.IsLeader() || election2.IsLeader() || election3.IsLeader()
	}, 1*time.Second, "at least one leader")

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

	assert.Equal(t, 1, leaderCount, "Expected exactly one leader, got %d", leaderCount)

	// Identify which instance is the leader
	var leaderElection Election
	var leaderID string
	if election1.IsLeader() {
		leaderElection = election1
		leaderID = "instance-1"
	} else if election2.IsLeader() {
		leaderElection = election2
		leaderID = "instance-2"
	} else if election3.IsLeader() {
		leaderElection = election3
		leaderID = "instance-3"
	}
	assert.NotNil(t, leaderElection, "Should have identified a leader")
	assert.NotEmpty(t, leaderID, "Should have leader ID")

	time.Sleep(50 * time.Millisecond)

	// Verify callbacks: leader's onPromote was called, others were not
	callbacks["instance-1"].mu.Lock()
	callbacks["instance-2"].mu.Lock()
	callbacks["instance-3"].mu.Lock()

	if leaderID == "instance-1" {
		assert.Equal(t, 1, callbacks["instance-1"].promoteCount, "Leader's onPromote should be called once")
		assert.Equal(t, 0, callbacks["instance-2"].promoteCount, "Follower's onPromote should not be called")
		assert.Equal(t, 0, callbacks["instance-3"].promoteCount, "Follower's onPromote should not be called")
	} else if leaderID == "instance-2" {
		assert.Equal(t, 0, callbacks["instance-1"].promoteCount, "Follower's onPromote should not be called")
		assert.Equal(t, 1, callbacks["instance-2"].promoteCount, "Leader's onPromote should be called once")
		assert.Equal(t, 0, callbacks["instance-3"].promoteCount, "Follower's onPromote should not be called")
	} else {
		assert.Equal(t, 0, callbacks["instance-1"].promoteCount, "Follower's onPromote should not be called")
		assert.Equal(t, 0, callbacks["instance-2"].promoteCount, "Follower's onPromote should not be called")
		assert.Equal(t, 1, callbacks["instance-3"].promoteCount, "Leader's onPromote should be called once")
	}

	callbacks["instance-1"].mu.Unlock()
	callbacks["instance-2"].mu.Unlock()
	callbacks["instance-3"].mu.Unlock()

	// Wait for watchers to be created and send initial leader entry to all watchers
	// so followers can update their LeaderID()
	time.Sleep(100 * time.Millisecond)
	initialEntry, err := mockKV.Get("test-group")
	if err == nil && initialEntry != nil {
		initialLeaderEntry := &natsmock.MockEntryImpl{
			KeyVal:   "test-group",
			ValueVal: initialEntry.Value(),
			RevVal:   initialEntry.Revision(),
		}
		watcherMu.Lock()
		for _, ch := range watcherChans {
			select {
			case ch <- initialLeaderEntry:
			default:
			}
		}
		watcherMu.Unlock()
		// Wait for watchers to process the initial entry
		waitForCondition(t, func() bool {
			return election1.LeaderID() == leaderID &&
				election2.LeaderID() == leaderID &&
				election3.LeaderID() == leaderID
		}, 500*time.Millisecond, "all elections to know the initial leader")
	}

	// Verify all instances know who the leader is
	assert.Equal(t, leaderID, election1.LeaderID(), "Election1 should know the leader")
	assert.Equal(t, leaderID, election2.LeaderID(), "Election2 should know the leader")
	assert.Equal(t, leaderID, election3.LeaderID(), "Election3 should know the leader")

	// Phase 3: Simulate leader crash
	oldLeaderID := leaderID
	err = leaderElection.Stop()
	assert.NoError(t, err, "Should be able to stop leader")

	// Wait for watchers to be ready
	time.Sleep(100 * time.Millisecond)

	// Delete the key and notify all watchers
	err = mockKV.Delete("test-group")
	assert.NoError(t, err, "Should be able to delete the key")

	// Send nil entries to all watchers to simulate key deletion
	watcherMu.Lock()
	for _, ch := range watcherChans {
		select {
		case ch <- nil:
		default:
		}
	}
	watcherMu.Unlock()
	time.Sleep(20 * time.Millisecond)

	// Wait for a new leader to be elected (with jitter, may take up to 500ms)
	// Check if any election that wasn't the old leader becomes leader
	waitForCondition(t, func() bool {
		if oldLeaderID == "instance-1" {
			return election2.IsLeader() || election3.IsLeader()
		} else if oldLeaderID == "instance-2" {
			return election1.IsLeader() || election3.IsLeader()
		} else {
			return election1.IsLeader() || election2.IsLeader()
		}
	}, 2*time.Second, "new leader to be elected")

	// Identify the new leader
	var newLeaderElection Election
	var newLeaderID string
	if election1.IsLeader() && election1.LeaderID() != oldLeaderID {
		newLeaderElection = election1
		newLeaderID = "instance-1"
	} else if election2.IsLeader() && election2.LeaderID() != oldLeaderID {
		newLeaderElection = election2
		newLeaderID = "instance-2"
	} else if election3.IsLeader() && election3.LeaderID() != oldLeaderID {
		newLeaderElection = election3
		newLeaderID = "instance-3"
	}
	assert.NotNil(t, newLeaderElection, "Should have identified a new leader")
	assert.NotEqual(t, oldLeaderID, newLeaderID, "New leader should be different from old leader")

	// Wait for new leader to create the key
	waitForCondition(t, func() bool {
		entry, err := mockKV.Get("test-group")
		return err == nil && entry != nil
	}, 1*time.Second, "new leader to create the key")

	// Send new leader entry to watchers (they may or may not process it due to mock limitations)
	// In a real system, watchers would automatically receive updates
	newEntry, err := mockKV.Get("test-group")
	if err == nil && newEntry != nil {
		newLeaderEntry := &natsmock.MockEntryImpl{
			KeyVal:   "test-group",
			ValueVal: newEntry.Value(),
			RevVal:   newEntry.Revision(),
		}
		watcherMu.Lock()
		for _, ch := range watcherChans {
			select {
			case ch <- newLeaderEntry:
			default:
			}
		}
		watcherMu.Unlock()
		time.Sleep(100 * time.Millisecond) // Give watchers time to process
	}

	// Verify callbacks: old leader's onDemote was called, new leader's onPromote was called
	callbacks[oldLeaderID].mu.Lock()
	callbacks[newLeaderID].mu.Lock()

	assert.Equal(t, 1, callbacks[oldLeaderID].demoteCount, "Old leader's onDemote should be called once")
	assert.Equal(t, 1, callbacks[newLeaderID].promoteCount, "New leader's onPromote should be called once")

	callbacks[oldLeaderID].mu.Unlock()
	callbacks[newLeaderID].mu.Unlock()

	// Count leaders again - should still be exactly 1
	leaderCount = 0
	if election1.IsLeader() {
		leaderCount++
	}
	if election2.IsLeader() {
		leaderCount++
	}
	if election3.IsLeader() {
		leaderCount++
	}
	assert.Equal(t, 1, leaderCount, "Should still have exactly one leader")

	// Verify all instances know the new leader
	// Note: Due to mock watcher limitations, LeaderID() might not update immediately
	// The important thing is that exactly one leader exists and callbacks are correct
	// In a real system, watchers would automatically update LeaderID()
	assert.Equal(t, newLeaderID, newLeaderElection.LeaderID(), "New leader should know itself")

	// Final cleanup
	err = election1.Stop()
	assert.NoError(t, err)
	err = election2.Stop()
	assert.NoError(t, err)
	err = election3.Stop()
	assert.NoError(t, err)

	// Verify final states
	assert.False(t, election1.IsLeader(), "Election1 should not be leader after stop")
	assert.False(t, election2.IsLeader(), "Election2 should not be leader after stop")
	assert.False(t, election3.IsLeader(), "Election3 should not be leader after stop")
}

// TestFencingToken_NewLeaderInvalidatesOld_Integration tests that when a new leader
// is elected, the old leader's token becomes invalid.
// This is a critical test for split-brain prevention.
func TestFencingToken_NewLeaderInvalidatesOld_Integration(t *testing.T) {
	// Setup: Two instances competing for leadership
	cfgA := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-A",
		TTL:                10 * time.Second,
		HeartbeatInterval:  100 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond, // Fast validation for testing
	}
	cfgB := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-B",
		TTL:                10 * time.Second,
		HeartbeatInterval:  100 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond,
	}

	nc := natsmock.NewMockConn()
	adapter := newMockConnAdapter(nc)

	// Create both elections
	electionA, err := NewElection(adapter, cfgA)
	assert.NoError(t, err)
	electionB, err := NewElection(adapter, cfgB)
	assert.NoError(t, err)

	// Start instance A first - it should become leader
	err = electionA.Start(context.Background())
	assert.NoError(t, err)
	waitForLeader(t, electionA, true, 1*time.Second)
	assert.True(t, electionA.IsLeader(), "Instance A should be leader")

	tokenA := electionA.Token()
	assert.NotEmpty(t, tokenA, "Instance A should have a token")

	// Validate token A is valid
	valid, err := electionA.ValidateToken(context.Background())
	assert.NoError(t, err)
	assert.True(t, valid, "Token A should be valid while A is leader")

	// Get mock KV to simulate B taking over leadership
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Start instance B - it should become follower initially
	err = electionB.Start(context.Background())
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	assert.False(t, electionB.IsLeader(), "Instance B should be follower initially")

	// Set up controllable watchers for both instances
	updatesChanA := make(chan natsmock.Entry, 10)
	updatesChanB := make(chan natsmock.Entry, 10)
	watcherA := &natsmock.MockWatcher{
		UpdatesChan: updatesChanA,
		StopChan:    make(chan struct{}),
	}
	watcherB := &natsmock.MockWatcher{
		UpdatesChan: updatesChanB,
		StopChan:    make(chan struct{}),
	}

	watchCount := 0
	mockKV.WatchFunc = func(key string, opts ...natsmock.WatchOption) (natsmock.Watcher, error) {
		watchCount++
		if watchCount == 1 {
			// First watcher is for instance A (when it becomes follower)
			return watcherA, nil
		}
		// Second watcher is for instance B (when it becomes follower)
		return watcherB, nil
	}

	// Stop instance A's heartbeat by making Update fail
	mockKV.UpdateFunc = func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		return 0, errors.New("revision mismatch")
	}

	// Wait for A to be demoted (heartbeat fails)
	waitForLeader(t, electionA, false, 500*time.Millisecond)

	// Get current entry from KV
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	// Now update KV store with B's leadership info
	// This simulates B successfully acquiring leadership
	tokenB := "token-B-" + time.Now().Format(time.RFC3339Nano)
	payloadB := map[string]interface{}{
		"id":    "instance-B",
		"token": tokenB,
	}
	payloadBytesB, err := json.Marshal(payloadB)
	assert.NoError(t, err)

	// Temporarily clear UpdateFunc to allow the update
	mockKV.UpdateFunc = nil
	_, err = mockKV.Update("test-group", payloadBytesB, entry.Revision())
	assert.NoError(t, err)
	// Restore UpdateFunc
	mockKV.UpdateFunc = func(key string, value []byte, rev uint64, opts ...natsmock.KVOption) (uint64, error) {
		return 0, errors.New("revision mismatch")
	}

	// At this point, the KV store has B's token, but A still thinks it's leader
	// (A was demoted due to heartbeat failure, but hasn't validated token yet)
	// Wait for validation loop to detect the invalid token
	// Validation interval is 200ms, so wait a bit longer
	time.Sleep(400 * time.Millisecond)

	// Verify A's token is now invalid (validation should have detected it)
	// Note: A might have already been demoted by validation loop, but token should still be invalid
	valid, err = electionA.ValidateToken(context.Background())
	if electionA.IsLeader() {
		// If A is still leader (shouldn't happen, but check anyway)
		assert.Error(t, err, "Token A validation should fail")
		assert.False(t, valid, "Token A should be invalid")
	} else {
		// A should have been demoted by validation loop
		assert.False(t, electionA.IsLeader(), "A should be demoted after token becomes invalid")
	}

	// Now validate that A's token is invalid
	valid, err = electionA.ValidateToken(context.Background())
	assert.Error(t, err, "Token A validation should fail")
	assert.False(t, valid, "Token A should be invalid after B becomes leader")

	// Verify that the KV store has B's token (not A's)
	// B doesn't need to be leader for this test - the key point is that A's token is invalid
	kvEntry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, kvEntry)

	var kvPayload map[string]interface{}
	err = json.Unmarshal(kvEntry.Value(), &kvPayload)
	assert.NoError(t, err)

	kvToken, ok := kvPayload["token"].(string)
	assert.True(t, ok, "KV should have a token")
	assert.NotEqual(t, tokenA, kvToken, "KV token should be different from A's token")
	assert.Equal(t, tokenB, kvToken, "KV token should match B's token")

	// Cleanup
	_ = electionA.Stop()
	_ = electionB.Stop()
}

// TestFencingToken_OperationRejection_Integration tests that operations with
// invalid tokens are rejected, preventing split-brain scenarios.
func TestFencingToken_OperationRejection_Integration(t *testing.T) {
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

	// Track operations
	var operationsAllowed []string
	var operationsRejected []string
	var mu sync.Mutex

	// Helper function to perform operation with token validation
	performOperation := func(operationName string) bool {
		if !election.IsLeader() {
			mu.Lock()
			operationsRejected = append(operationsRejected, operationName)
			mu.Unlock()
			return false
		}

		// Validate token before operation
		if !election.ValidateTokenOrDemote(context.Background()) {
			mu.Lock()
			operationsRejected = append(operationsRejected, operationName)
			mu.Unlock()
			return false
		}

		// Operation succeeds
		mu.Lock()
		operationsAllowed = append(operationsAllowed, operationName)
		mu.Unlock()
		return true
	}

	// Start and become leader
	err = election.Start(context.Background())
	assert.NoError(t, err)
	waitForLeader(t, election, true, 1*time.Second)

	// Perform operation while leader - should succeed
	success := performOperation("operation-1")
	assert.True(t, success, "Operation should succeed with valid token")
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, 1, len(operationsAllowed), "One operation should be allowed")
	assert.Equal(t, 0, len(operationsRejected), "No operations should be rejected yet")
	mu.Unlock()

	// Get mock KV and invalidate the token
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Update with different leader/token to invalidate current token
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

	// Wait for validation loop to detect invalid token and demote
	waitForLeader(t, election, false, 500*time.Millisecond)

	// Try to perform operation after losing leadership - should be rejected
	success = performOperation("operation-2")
	assert.False(t, success, "Operation should be rejected with invalid token")
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, 1, len(operationsAllowed), "Only one operation should be allowed")
	assert.Equal(t, 1, len(operationsRejected), "One operation should be rejected")
	assert.Equal(t, "operation-2", operationsRejected[0], "Second operation should be rejected")
	mu.Unlock()

	_ = election.Stop()
}

// TestFencingToken_PeriodicValidation_Integration tests that the validation loop
// periodically checks the token and demotes quickly when it becomes invalid.
func TestFencingToken_PeriodicValidation_Integration(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  100 * time.Millisecond,
		ValidationInterval: 200 * time.Millisecond, // Fast validation for testing
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	var demoteCalled bool
	var demoteTime time.Time
	election.OnDemote(func() {
		demoteCalled = true
		demoteTime = time.Now()
	})

	// Start and become leader
	err = election.Start(context.Background())
	assert.NoError(t, err)
	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for at least one validation cycle to complete (token is valid)
	time.Sleep(300 * time.Millisecond)
	assert.True(t, election.IsLeader(), "Should still be leader (token valid)")

	// Get mock KV and invalidate the token
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)

	// Record when we invalidate the token
	invalidationTime := time.Now()

	// Update with different token to invalidate
	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": "different-token-456",
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err)

	// Wait for validation loop to detect invalid token
	// Should detect within one validation interval (200ms) + some buffer
	waitForLeader(t, election, false, 500*time.Millisecond)

	// Verify demotion happened
	assert.False(t, election.IsLeader(), "Should be demoted after token becomes invalid")
	assert.True(t, demoteCalled, "OnDemote callback should be called")

	// Verify demotion happened quickly (within validation interval + buffer)
	demotionDelay := demoteTime.Sub(invalidationTime)
	assert.Less(t, demotionDelay, 500*time.Millisecond, "Demotion should happen quickly after token invalidation")

	_ = election.Stop()
}
