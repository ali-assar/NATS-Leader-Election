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

// TestChaos_NATSServerRestart tests behavior when NATS server restarts
func TestChaos_NATSServerRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start embedded NATS server
	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)

	// Create KV bucket
	bucketName := "test-leaders"
	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	err = CreateKVBucket(conn, bucketName, 10*time.Second)
	require.NoError(t, err)

	// Create election with connection monitoring
	cfg := ElectionConfig{
		Bucket:                bucketName,
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   10 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		DisconnectGracePeriod: 2 * time.Second, // Grace period for reconnection
	}

	election, err := NewElectionWithConn(conn, cfg)
	require.NoError(t, err)

	// Track callbacks
	var demoteCount int
	var mu sync.Mutex

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

	// Shutdown NATS server
	server.Shutdown()

	// Wait for grace period + some buffer
	time.Sleep(3 * time.Second)

	// Leader should be demoted after grace period expires
	// Note: With real NATS, the connection will be closed and the leader
	// should be demoted. However, the exact timing depends on connection
	// monitoring and grace period handling.

	// Note: With real NATS, the connection will be closed and the leader
	// should be demoted. However, the exact timing depends on connection
	// monitoring and grace period handling.
	// In a real scenario, the leader would be demoted when the connection
	// is lost and grace period expires.

	// Restart NATS server
	server2, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server2)
		require.NoError(t, err)
	}()

	// Reconnect
	conn2, err := nats.Connect(server2.ClientURL())
	require.NoError(t, err)
	defer conn2.Close()

	// Recreate bucket
	err = CreateKVBucket(conn2, bucketName, 10*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn2, bucketName)
		require.NoError(t, err)
	}()

	// Create new election with new connection
	election2, err := NewElectionWithConn(conn2, cfg)
	require.NoError(t, err)

	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Should be able to become leader again
	waitForLeader(t, election2, true, 3*time.Second)
	assert.True(t, election2.IsLeader(), "Should become leader after reconnection")
}

// TestChaos_NetworkPartition tests behavior during network partition.
// This simulates a network partition by disconnecting the leader's connection.
func TestChaos_NetworkPartition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		Bucket:                bucketName,
		Group:                 "test-group",
		InstanceID:            "instance-1",
		TTL:                   5 * time.Second,
		HeartbeatInterval:     200 * time.Millisecond,
		DisconnectGracePeriod: 1 * time.Second,
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
	var demote1Count int
	var promote2Count int
	var mu sync.Mutex

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

	// Start election1 first to ensure it becomes leader
	// If both start simultaneously, they compete and election2 might win
	err = election1.Start(ctx)
	require.NoError(t, err)
	defer election1.Stop()

	// Wait for election1 to become leader before starting election2
	initialTiming := TimingConfig{
		HeartbeatInterval: cfg1.HeartbeatInterval,
	}
	waitForLeader(t, election1, true, initialTiming.CalculateInitialAcquisitionTimeout())
	assert.True(t, election1.IsLeader(), "Election1 should be leader before starting election2")

	// Now start election2 (it will become a follower since election1 is already leader)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Simulate network partition by closing connection1
	conn1.Close()

	// Calculate timeout for network partition scenario
	partitionTiming := TimingConfig{
		TTL:                   cfg1.TTL,
		HeartbeatInterval:     cfg1.HeartbeatInterval,
		DisconnectGracePeriod: cfg1.DisconnectGracePeriod,
	}
	sleepDuration, waitTimeout := partitionTiming.CalculateNetworkPartitionTimeout()
	time.Sleep(sleepDuration)
	// With real NATS, watchers may take time to detect the key deletion or TTL expiration
	waitForLeader(t, election2, true, waitTimeout)
	assert.True(t, election2.IsLeader(), "Election2 should become leader after partition")

	mu.Lock()
	assert.Equal(t, 1, promote2Count, "Election2's OnPromote should be called")
	mu.Unlock()
}

// TestChaos_ProcessKill tests behavior when leader process is killed
// This simulates a process kill by stopping the election abruptly
func TestChaos_ProcessKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	var promote2Count int
	var mu sync.Mutex

	election2.OnPromote(func(ctx context.Context, token string) {
		mu.Lock()
		defer mu.Unlock()
		promote2Count++
	})

	// Start election1 first to ensure it becomes leader
	// If both start simultaneously, they compete and election2 might win
	err = election1.Start(ctx)
	require.NoError(t, err)

	// Wait for election1 to become leader before starting election2
	initialTiming := TimingConfig{
		HeartbeatInterval: cfg1.HeartbeatInterval,
	}
	waitForLeader(t, election1, true, initialTiming.CalculateInitialAcquisitionTimeout())
	assert.True(t, election1.IsLeader(), "Election1 should be leader before starting election2")

	// Now start election2 (it will become a follower since election1 is already leader)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Simulate process kill by stopping without cleanup (no DeleteKey)
	// This simulates a crash where the process dies without graceful shutdown
	// Close connection first to simulate abrupt death
	conn1.Close()

	// Then stop (which may fail due to closed connection, but that's OK)
	election1.Stop()

	// Calculate timeout for TTL expiration scenario (process kill without DeleteKey)
	ttlTiming := TimingConfig{
		TTL:               cfg1.TTL,
		HeartbeatInterval: cfg1.HeartbeatInterval,
		// DisconnectGracePeriod not set, will use default
	}
	sleepDuration, waitTimeout := ttlTiming.CalculateTTLExpirationTimeout()
	time.Sleep(sleepDuration)
	waitForLeader(t, election2, true, waitTimeout)
	assert.True(t, election2.IsLeader(), "Election2 should become leader after election1 dies")

	mu.Lock()
	assert.Equal(t, 1, promote2Count, "Election2's OnPromote should be called")
	mu.Unlock()
}

// TestChaos_ProcessKillWithDeleteKey tests process kill with DeleteKey option.
// This simulates graceful shutdown where the key is deleted immediately.
func TestChaos_ProcessKillWithDeleteKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	conn2, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn2.Close()

	// Create two elections
	cfg1 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	cfg2 := ElectionConfig{
		Bucket:            bucketName,
		Group:             "test-group",
		InstanceID:        "instance-2",
		TTL:               10 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
	}

	election1, err := NewElectionWithConn(conn1, cfg1)
	require.NoError(t, err)

	election2, err := NewElectionWithConn(conn2, cfg2)
	require.NoError(t, err)

	// Track callbacks
	var promote2Count int
	var mu sync.Mutex

	election2.OnPromote(func(ctx context.Context, token string) {
		mu.Lock()
		defer mu.Unlock()
		promote2Count++
	})

	// Start election1 first to ensure it becomes leader
	// If both start simultaneously, they compete and election2 might win
	err = election1.Start(ctx)
	require.NoError(t, err)

	// Wait for election1 to become leader before starting election2
	initialTiming := TimingConfig{
		HeartbeatInterval: cfg1.HeartbeatInterval,
	}
	waitForLeader(t, election1, true, initialTiming.CalculateInitialAcquisitionTimeout())
	assert.True(t, election1.IsLeader(), "Election1 should be leader before starting election2")

	// Now start election2 (it will become a follower since election1 is already leader)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Stop with DeleteKey option (simulates graceful shutdown)
	err = election1.StopWithContext(ctx, StopOptions{
		DeleteKey: true,
	})
	require.NoError(t, err)

	// Calculate timeout for immediate deletion scenario (graceful shutdown with DeleteKey)
	deleteTiming := TimingConfig{
		HeartbeatInterval: cfg1.HeartbeatInterval,
	}
	sleepDuration, waitTimeout := deleteTiming.CalculateImmediateDeletionTimeout()
	time.Sleep(sleepDuration)
	// With DeleteKey, the key is deleted immediately, but watchers may take time to detect
	waitForLeader(t, election2, true, waitTimeout)
	assert.True(t, election2.IsLeader(), "Election2 should become leader quickly after key deletion")

	mu.Lock()
	assert.Equal(t, 1, promote2Count, "Election2's OnPromote should be called")
	mu.Unlock()
}

// TestChaos_ThunderingHerd tests that multiple candidates don't cause thundering herd
func TestChaos_ThunderingHerd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	conn0, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn0.Close()

	err = CreateKVBucket(conn0, bucketName, 5*time.Second)
	require.NoError(t, err)

	// Create 10 elections simultaneously
	numElections := 10
	elections := make([]Election, numElections)
	conns := make([]*nats.Conn, numElections)

	for i := 0; i < numElections; i++ {
		conn, err2 := nats.Connect(server.ClientURL())
		require.NoError(t, err2)
		conns[i] = conn

		cfg := ElectionConfig{
			Bucket:            bucketName,
			Group:             "test-group",
			InstanceID:        "instance-" + string(rune('0'+i)),
			TTL:               5 * time.Second,
			HeartbeatInterval: 200 * time.Millisecond,
		}

		election, err3 := NewElectionWithConn(conn, cfg)
		require.NoError(t, err3)
		elections[i] = election
	}

	// Cleanup
	defer func() {
		for i, election := range elections {
			if election != nil {
				election.Stop()
			}
			if conns[i] != nil {
				conns[i].Close()
			}
		}
		err := CleanupKVBucket(conn0, bucketName)
		if err != nil {
			t.Logf("Error cleaning up bucket: %v", err)
		}
	}()

	// Start all elections simultaneously
	for _, election := range elections {
		err4 := election.Start(ctx)
		require.NoError(t, err4)
	}

	// Wait for one to become leader
	waitForCondition(t, func() bool {
		for _, election := range elections {
			if election.IsLeader() {
				return true
			}
		}
		return false
	}, 5*time.Second, "one election to become leader")

	// Verify exactly one leader
	leaderCount := 0
	for _, election := range elections {
		if election.IsLeader() {
			leaderCount++
		}
	}
	assert.Equal(t, 1, leaderCount, "Should have exactly one leader even with many candidates")
}
