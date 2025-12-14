package leader

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

func TestAttemptAcquire_Success(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	nc := natsmock.NewMockConn()
	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	assert.NoError(t, err)
	assert.NotNil(t, election)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	WaitForLeader(t, election, true, 1*time.Second)

	assert.True(t, election.IsLeader(), "Expected to be leader")
	assert.Equal(t, "instance-1", election.LeaderID())
	assert.NotEmpty(t, election.Token(), "Expected to have a token")
}

func TestAttemptAcquire_KeyExists(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	nc := natsmock.NewMockConn()
	js, _ := nc.JetStream()
	kv, _ := js.KeyValue("leaders")
	payload := []byte(`{"id":"other-instance","token":"existing-token"}`)
	_, err := kv.Create("test-group", payload)
	assert.NoError(t, err)

	election, err := NewElection(NewMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	WaitForLeader(t, election, false, 1*time.Second)

	assert.False(t, election.IsLeader(), "Expected NOT to be leader")
	assert.Empty(t, election.LeaderID())
	assert.Empty(t, election.Token())
}

func TestAttemptAcquire_Concurrent(t *testing.T) {
	cfg1 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	cfg2 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-2",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	cfg3 := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-3",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	// All instances share the same mock connection (same KV store)
	nc := natsmock.NewMockConn()
	adapter := NewMockConnAdapter(nc)

	// Create all elections
	election1, err := NewElection(adapter, cfg1)
	assert.NoError(t, err)
	election2, err := NewElection(adapter, cfg2)
	assert.NoError(t, err)
	election3, err := NewElection(adapter, cfg3)
	assert.NoError(t, err)

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

	// Wait for all attemptAcquire goroutines to complete
	// At least one should become leader
	WaitForCondition(t, func() bool {
		return election1.IsLeader() || election2.IsLeader() || election3.IsLeader()
	}, 1*time.Second, "at least one leader")

	// Count how many became leader (should be exactly 1)
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
}

// TestStart_AlreadyStarted tests that Start() returns error if already started
func TestStart_AlreadyStarted(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	nc := natsmock.NewMockConn()
	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	assert.NoError(t, err)

	// Start first time - should succeed
	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Try to start again - should fail
	err = election.Start(context.Background())
	assert.Error(t, err)
	assert.Equal(t, ErrAlreadyStarted, err)
}

// TestOnPromote_Callback tests that OnPromote callback is called when becoming leader
func TestOnPromote_Callback(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	nc := natsmock.NewMockConn()
	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	assert.NoError(t, err)

	// Track if callback was called
	var callbackMu sync.Mutex
	var callbackCalled bool
	var callbackToken string
	var callbackCtx context.Context

	election.OnPromote(func(ctx context.Context, token string) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackCalled = true
		callbackToken = token
		callbackCtx = ctx
	})

	// Start the election
	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and callback to be called
	WaitForCondition(t, func() bool {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		return callbackCalled
	}, 1*time.Second, "OnPromote callback")

	// Check callback was called
	callbackMu.Lock()
	wasCalled := callbackCalled
	token := callbackToken
	ctx := callbackCtx
	callbackMu.Unlock()
	assert.True(t, wasCalled, "OnPromote callback should be called")
	assert.NotEmpty(t, token, "Callback should receive a token")
	assert.NotNil(t, ctx, "Callback should receive a context")
	assert.Equal(t, election.Token(), token, "Callback token should match election token")
}

// TestOnDemote_Callback tests that OnDemote callback is called when losing leadership
func TestOnDemote_Callback(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}

	nc := natsmock.NewMockConn()
	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	assert.NoError(t, err)

	// Track if callback was called
	var demoteCalled bool

	election.OnDemote(func() {
		demoteCalled = true
	})

	// Start and become leader
	err = election.Start(context.Background())
	assert.NoError(t, err)
	WaitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader())

	// Stop the election (should trigger demote)
	err = election.Stop()
	assert.NoError(t, err)

	// Wait for callback to be called
	WaitForCondition(t, func() bool {
		return demoteCalled
	}, 500*time.Millisecond, "OnDemote callback")

	// Check callback was called
	assert.True(t, demoteCalled, "OnDemote callback should be called on stop")
}

// TestNewElection_InvalidConfig tests that NewElection returns error for invalid config
func TestNewElection_InvalidConfig(t *testing.T) {
	nc := natsmock.NewMockConn()

	tests := []struct {
		name string
		cfg  ElectionConfig
	}{
		{
			name: "empty bucket",
			cfg: ElectionConfig{
				Bucket:            "",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
		},
		{
			name: "TTL too short",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               2 * time.Second, // Too short (less than 3x heartbeat)
				HeartbeatInterval: 2 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewMockConnAdapter(nc)
		_, err := NewElection(adapter, tt.cfg)
			assert.Error(t, err, "Expected error for invalid config: %s", tt.name)
		})
	}
}

// Mock adapters are now in test_adapters.go
// Use NewMockConnAdapter() instead of newMockConnAdapter()
