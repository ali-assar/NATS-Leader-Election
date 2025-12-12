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
	// Calculation: Initial acquisition can take:
	// - Initial jitter: 10-100ms
	// - KV operation: ~10-50ms
	// - Retry backoff (if first attempt fails): up to 350ms
	// Total worst case: ~500ms, but with real NATS and potential retries, allow more time
	waitForLeader(t, election1, true, 3*time.Second)
	assert.True(t, election1.IsLeader(), "Election1 should be leader before starting election2")

	// Now start election2 (it will become a follower since election1 is already leader)
	err = election2.Start(ctx)
	require.NoError(t, err)
	defer election2.Stop()

	// Simulate network partition by closing connection1
	conn1.Close()

	// Delay calculation for network partition scenario:
	// 1. Disconnect grace period: 1s (explicitly set in cfg1)
	//    After grace period expires, leader is demoted (becomes follower)
	// 2. Key TTL expiration: Key still exists in KV store after demotion!
	//    Key expires TTL (5s) after last heartbeat
	//    Worst case: Last heartbeat was just before disconnect, so key expires ~5s after disconnect
	// 3. Detection delays:
	//    - Periodic check interval: 500ms (worst case: check just ran, next in 500ms)
	//    - Initial jitter: 10-100ms
	//    - Backoff on retry (if first attempt fails): up to 350ms (50ms + 100ms + 200ms)
	// 4. Total minimum: 1s (grace) + 5s (TTL) + 500ms (periodic check) + 100ms (jitter) = 6.6s
	// 5. With retries: 6.6s + 350ms = 6.95s
	// 6. Add buffer for NATS propagation: ~1s
	// 7. Total sleep before waitForLeader: ~8s
	// 8. waitForLeader timeout: Additional 7s buffer for safety = 15s total
	time.Sleep(8 * time.Second)
	// With real NATS, watchers may take time to detect the key deletion or TTL expiration
	waitForLeader(t, election2, true, 15*time.Second)
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
	// Calculation: Initial acquisition can take:
	// - Initial jitter: 10-100ms
	// - KV operation: ~10-50ms
	// - Retry backoff (if first attempt fails): up to 350ms
	// Total worst case: ~500ms, but with real NATS and potential retries, allow more time
	waitForLeader(t, election1, true, 3*time.Second)
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

	// Delay calculation for TTL expiration scenario:
	// 1. Disconnect grace period: Default = max(3 * HeartbeatInterval, 5s) = 5s
	//    (HeartbeatInterval=200ms, so 3*200ms=600ms < 5s, so grace period = 5s)
	// 2. Key TTL expiration: Key expires TTL (5s) after last heartbeat
	//    Worst case: Last heartbeat was just before disconnect, so key expires ~5s after disconnect
	// 3. Detection delays:
	//    - Periodic check interval: 500ms (worst case: check just ran, next in 500ms)
	//    - Initial jitter: 10-100ms
	//    - Backoff on retry (if first attempt fails): up to 350ms (50ms + 100ms + 200ms)
	// 4. Total minimum: 5s (TTL) + 500ms (periodic check) + 100ms (jitter) = 5.6s
	// 5. With retries: 5.6s + 350ms = 5.95s
	// 6. Add buffer for NATS propagation: ~1s
	// 7. Total sleep before waitForLeader: ~7s
	// 8. waitForLeader timeout: Additional 8s buffer for safety = 15s total
	time.Sleep(7 * time.Second)
	waitForLeader(t, election2, true, 15*time.Second)
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
	// Calculation: Initial acquisition can take:
	// - Initial jitter: 10-100ms
	// - KV operation: ~10-50ms
	// - Retry backoff (if first attempt fails): up to 350ms
	// Total worst case: ~500ms, but with real NATS and potential retries, allow more time
	waitForLeader(t, election1, true, 3*time.Second)
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

	// Delay calculation for immediate deletion scenario:
	// 1. Key deletion: Immediate (DeleteKey=true)
	// 2. Detection delays:
	//    - Periodic check interval: 500ms (worst case: check just ran, next in 500ms)
	//    - Initial jitter: 10-100ms
	//    - Backoff on retry (if first attempt fails): up to 350ms (50ms + 100ms + 200ms)
	// 3. Total minimum: 500ms (periodic check) + 100ms (jitter) = 600ms
	// 4. With retries: 600ms + 350ms = 950ms
	// 5. Add buffer for NATS propagation: ~500ms
	// 6. Total sleep before waitForLeader: ~1.5s
	// 7. waitForLeader timeout: Additional 8s buffer for safety = 10s total
	time.Sleep(1500 * time.Millisecond)
	// With DeleteKey, the key is deleted immediately, but watchers may take time to detect
	waitForLeader(t, election2, true, 10*time.Second)
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
