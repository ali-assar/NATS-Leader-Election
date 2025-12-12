package leader

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealNATS_FullElectionCycle tests the complete election lifecycle with a real NATS server
func TestRealNATS_FullElectionCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	// Connect to NATS
	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	// Create KV bucket
	bucketName := "test-leaders"
	err = CreateKVBucket(conn, bucketName, 10*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn, bucketName)
		require.NoError(t, err)
	}()

	// Create election
	cfg := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	election, err := NewElectionWithConn(conn, cfg)
	require.NoError(t, err)

	// Track callbacks
	var promoteCount int
	var demoteCount int
	var promoteTokens []string
	var mu sync.Mutex

	election.OnPromote(func(ctx context.Context, token string) {
		mu.Lock()
		defer mu.Unlock()
		promoteCount++
		promoteTokens = append(promoteTokens, token)
	})

	election.OnDemote(func() {
		mu.Lock()
		defer mu.Unlock()
		demoteCount++
	})

	// Start election
	err = election.Start(ctx)
	require.NoError(t, err)
	defer election.Stop()

	// Wait to become leader
	waitForLeader(t, election, true, 2*time.Second)
	assert.True(t, election.IsLeader(), "Should become leader")
	assert.NotEmpty(t, election.Token(), "Should have a token")
	assert.Equal(t, "instance-1", election.LeaderID(), "Leader ID should match")

	// Wait for OnPromote callback
	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promoteCount > 0
	}, 2*time.Second, "OnPromote callback")

	mu.Lock()
	assert.Equal(t, 1, promoteCount, "OnPromote should be called once")
	assert.Equal(t, 1, len(promoteTokens), "Should have one token")
	assert.Equal(t, election.Token(), promoteTokens[0], "Token should match")
	mu.Unlock()

	// Verify leader is maintaining leadership
	time.Sleep(500 * time.Millisecond)
	assert.True(t, election.IsLeader(), "Should still be leader")

	// Stop election (simulate leader crash)
	err = election.Stop()
	require.NoError(t, err)

	// Wait a bit for cleanup
	time.Sleep(100 * time.Millisecond)

	// Verify OnDemote was called
	mu.Lock()
	assert.GreaterOrEqual(t, demoteCount, 1, "OnDemote should be called on stop")
	mu.Unlock()

	assert.False(t, election.IsLeader(), "Should not be leader after stop")
}

// TestRealNATS_MultipleCandidates tests multiple candidates competing for leadership
func TestRealNATS_MultipleCandidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	// Create KV bucket
	bucketName := "test-leaders"
	conn1, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn1.Close()

	err = CreateKVBucket(conn1, bucketName, 10*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn1, bucketName)
		require.NoError(t, err)
	}()

	// Create three elections
	cfg1 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	cfg2 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-2",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	cfg3 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-3",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	conn2, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn2.Close()

	conn3, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn3.Close()

	election1, err := NewElectionWithConn(conn1, cfg1)
	require.NoError(t, err)

	election2, err := NewElectionWithConn(conn2, cfg2)
	require.NoError(t, err)

	election3, err := NewElectionWithConn(conn3, cfg3)
	require.NoError(t, err)

	// Track callbacks
	type callbackTracker struct {
		mu           sync.Mutex
		promoteCount int
		demoteCount  int
	}

	callbacks := map[string]*callbackTracker{
		"instance-1": {},
		"instance-2": {},
		"instance-3": {},
	}

	election1.OnPromote(func(ctx context.Context, token string) {
		callbacks["instance-1"].mu.Lock()
		defer callbacks["instance-1"].mu.Unlock()
		callbacks["instance-1"].promoteCount++
	})

	election1.OnDemote(func() {
		callbacks["instance-1"].mu.Lock()
		defer callbacks["instance-1"].mu.Unlock()
		callbacks["instance-1"].demoteCount++
	})

	election2.OnPromote(func(ctx context.Context, token string) {
		callbacks["instance-2"].mu.Lock()
		defer callbacks["instance-2"].mu.Unlock()
		callbacks["instance-2"].promoteCount++
	})

	election2.OnDemote(func() {
		callbacks["instance-2"].mu.Lock()
		defer callbacks["instance-2"].mu.Unlock()
		callbacks["instance-2"].demoteCount++
	})

	election3.OnPromote(func(ctx context.Context, token string) {
		callbacks["instance-3"].mu.Lock()
		defer callbacks["instance-3"].mu.Unlock()
		callbacks["instance-3"].promoteCount++
	})

	election3.OnDemote(func() {
		callbacks["instance-3"].mu.Lock()
		defer callbacks["instance-3"].mu.Unlock()
		callbacks["instance-3"].demoteCount++
	})

	// Start all elections
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	err = election3.Start(ctx)
	require.NoError(t, err)
	defer election3.Stop()

	// Wait for one to become leader
	waitForCondition(t, func() bool {
		return election1.IsLeader() || election2.IsLeader() || election3.IsLeader()
	}, 3*time.Second, "one election to become leader")

	// Verify exactly one leader
	leaderCount := 0
	var leaderID string
	if election1.IsLeader() {
		leaderCount++
		leaderID = "instance-1"
	}
	if election2.IsLeader() {
		leaderCount++
		leaderID = "instance-2"
	}
	if election3.IsLeader() {
		leaderCount++
		leaderID = "instance-3"
	}

	assert.Equal(t, 1, leaderCount, "Should have exactly one leader")
	assert.NotEmpty(t, leaderID, "Should have a leader ID")

	// Wait for callbacks
	time.Sleep(500 * time.Millisecond)

	// Verify only leader's OnPromote was called
	callbacks[leaderID].mu.Lock()
	assert.Equal(t, 1, callbacks[leaderID].promoteCount, "Leader's OnPromote should be called")
	callbacks[leaderID].mu.Unlock()

	// Verify followers know the leader and watchers are running
	waitForCondition(t, func() bool {
		return election1.LeaderID() == leaderID &&
			election2.LeaderID() == leaderID &&
			election3.LeaderID() == leaderID
	}, 3*time.Second, "all elections to know the leader")

	assert.Equal(t, leaderID, election1.LeaderID(), "Election1 should know the leader")
	assert.Equal(t, leaderID, election2.LeaderID(), "Election2 should know the leader")
	assert.Equal(t, leaderID, election3.LeaderID(), "Election3 should know the leader")

	// Ensure watchers have time to start and are watching
	// Wait for at least one heartbeat cycle to ensure everything is stable
	// This ensures followers have started their watchers
	time.Sleep(500 * time.Millisecond)

	// Stop the leader
	var leaderElection Election
	var followerElections []Election
	if election1.IsLeader() {
		leaderElection = election1
		followerElections = []Election{election2, election3}
	} else if election2.IsLeader() {
		leaderElection = election2
		followerElections = []Election{election1, election3}
	} else {
		leaderElection = election3
		followerElections = []Election{election1, election2}
	}

	// Verify followers are not leaders before stopping
	for _, follower := range followerElections {
		assert.False(t, follower.IsLeader(), "Follower should not be leader before leader stops")
	}

	// Get KV store to verify deletion
	js, err := conn1.JetStream()
	require.NoError(t, err)
	kv, err := js.KeyValue(bucketName)
	require.NoError(t, err)

	// Verify key exists before deletion
	_, err = kv.Get("test-group")
	require.NoError(t, err, "Key should exist before leader stops")

	// Stop with DeleteKey for faster failover
	err = leaderElection.StopWithContext(ctx, StopOptions{
		DeleteKey: true,
	})
	require.NoError(t, err)

	// Wait for NATS to propagate the deletion and verify it's gone
	// This confirms DeleteKey worked
	waitForCondition(t, func() bool {
		_, err := kv.Get("test-group")
		return err != nil // Key should not exist (error means deleted)
	}, 2*time.Second, "key to be deleted")

	// Wait for new leader to be elected
	// The periodic check in watchLoop runs every 500ms, so we need to wait at least that long
	// Plus jitter (up to 100ms) + backoff + acquisition time
	waitForCondition(t, func() bool {
		leaderCount := 0
		for _, follower := range followerElections {
			if follower.IsLeader() {
				leaderCount++
			}
		}
		return leaderCount == 1
	}, 15*time.Second, "new leader to be elected")

	// Verify exactly one new leader
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
	assert.Equal(t, 1, leaderCount, "Should have exactly one new leader")
}

// TestRealNATS_LeaderTakeover tests leader takeover when old leader stops.
// Note: This test may be flaky with real NATS due to watcher timing.
func TestRealNATS_LeaderTakeover(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping flaky integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	// Create KV bucket
	bucketName := "test-leaders"
	conn1, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn1.Close()

	err = CreateKVBucket(conn1, bucketName, 5*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn1, bucketName)
		require.NoError(t, err)
	}()

	conn2, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn2.Close()

	// Create two elections
	cfg1 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	cfg2 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-2",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	election1, err := NewElectionWithConn(conn1, cfg1)
	require.NoError(t, err)

	election2, err := NewElectionWithConn(conn2, cfg2)
	require.NoError(t, err)

	// Track callbacks
	var promote1Count, demote1Count int
	var promote2Count, demote2Count int
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

	election2.OnDemote(func() {
		mu.Lock()
		defer mu.Unlock()
		demote2Count++
	})

	// Start first election
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader
	waitForLeader(t, election1, true, 2*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader")

	// Start second election (should be follower)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	time.Sleep(500 * time.Millisecond)
	assert.False(t, election2.IsLeader(), "Election2 should be follower")

	// Stop election1 with DeleteKey for faster failover
	err = election1.StopWithContext(ctx, StopOptions{
		DeleteKey: true,
	})
	require.NoError(t, err)

	// Give watchers time to detect the key deletion
	time.Sleep(200 * time.Millisecond)

	// Wait for election2 to take over (with DeleteKey, should be faster)
	// Real NATS watchers may take time to detect key deletion
	waitForLeader(t, election2, true, 10*time.Second)
	assert.True(t, election2.IsLeader(), "Election2 should become leader after election1 stops")

	// Verify callbacks
	mu.Lock()
	assert.Equal(t, 1, promote1Count, "Election1's OnPromote should be called once")
	assert.GreaterOrEqual(t, demote1Count, 1, "Election1's OnDemote should be called")
	assert.Equal(t, 1, promote2Count, "Election2's OnPromote should be called once")
	mu.Unlock()
}

// TestRealNATS_HeartbeatMaintainsLeadership tests that heartbeats maintain leadership
func TestRealNATS_HeartbeatMaintainsLeadership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	// Create KV bucket
	bucketName := "test-leaders"
	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	err = CreateKVBucket(conn, bucketName, 5*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn, bucketName)
		require.NoError(t, err)
	}()

	// Create election with short heartbeat interval
	cfg := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               5 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	election, err := NewElectionWithConn(conn, cfg)
	require.NoError(t, err)

	// Start election
	err = election.Start(ctx)
	require.NoError(t, err)
	defer election.Stop()

	// Wait to become leader
	waitForLeader(t, election, true, 2*time.Second)
	assert.True(t, election.IsLeader(), "Should become leader")

	initialToken := election.Token()
	assert.NotEmpty(t, initialToken, "Should have a token")

	// Wait for multiple heartbeat intervals
	time.Sleep(1 * time.Second)

	// Verify still leader and token hasn't changed
	assert.True(t, election.IsLeader(), "Should still be leader after heartbeats")
	assert.Equal(t, initialToken, election.Token(), "Token should not change during heartbeats")
}

// TestRealNATS_TokenValidation tests that token validation works correctly
func TestRealNATS_TokenValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	// Create KV bucket
	bucketName := "test-leaders"
	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	err = CreateKVBucket(conn, bucketName, 5*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn, bucketName)
		require.NoError(t, err)
	}()

	// Create election
	cfg := ElectionConfig{
		Bucket:             bucketName,
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                5 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond,
		ValidationInterval: 300 * time.Millisecond,
	}

	election, err := NewElectionWithConn(conn, cfg)
	require.NoError(t, err)

	// Start election
	err = election.Start(ctx)
	require.NoError(t, err)
	defer election.Stop()

	// Wait to become leader
	waitForLeader(t, election, true, 2*time.Second)
	assert.True(t, election.IsLeader(), "Should become leader")

	token := election.Token()
	assert.NotEmpty(t, token, "Should have a token")

	// Validate token (should be valid)
	valid, err := election.ValidateToken(ctx)
	require.NoError(t, err)
	assert.True(t, valid, "Token should be valid")

	// Wait for validation loop to run
	time.Sleep(500 * time.Millisecond)

	// Should still be leader (token is valid)
	assert.True(t, election.IsLeader(), "Should still be leader with valid token")
}
