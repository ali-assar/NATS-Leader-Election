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
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, election)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)

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

	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, false, 1*time.Second)

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
	adapter := newMockConnAdapter(nc)

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
	waitForCondition(t, func() bool {
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
	election, err := NewElection(newMockConnAdapter(nc), cfg)
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
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	// Track if callback was called
	var callbackCalled bool
	var callbackToken string
	var callbackCtx context.Context

	election.OnPromote(func(ctx context.Context, token string) {
		callbackCalled = true
		callbackToken = token
		callbackCtx = ctx
	})

	// Start the election
	err = election.Start(context.Background())
	assert.NoError(t, err)

	// Wait for attemptAcquire to complete and callback to be called
	waitForCondition(t, func() bool {
		return callbackCalled
	}, 1*time.Second, "OnPromote callback")

	// Check callback was called
	assert.True(t, callbackCalled, "OnPromote callback should be called")
	assert.NotEmpty(t, callbackToken, "Callback should receive a token")
	assert.NotNil(t, callbackCtx, "Callback should receive a context")
	assert.Equal(t, election.Token(), callbackToken, "Callback token should match election token")
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
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	// Track if callback was called
	var demoteCalled bool

	election.OnDemote(func() {
		demoteCalled = true
	})

	// Start and become leader
	err = election.Start(context.Background())
	assert.NoError(t, err)
	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader())

	// Stop the election (should trigger demote)
	err = election.Stop()
	assert.NoError(t, err)

	// Wait for callback to be called
	waitForCondition(t, func() bool {
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
			_, err := NewElection(newMockConnAdapter(nc), tt.cfg)
			assert.Error(t, err, "Expected error for invalid config: %s", tt.name)
		})
	}
}

// mockKeyValueAdapter adapts *natsmock.MockKeyValue to leader.KeyValue interface
type mockKeyValueAdapter struct {
	kv *natsmock.MockKeyValue
}

func (a *mockKeyValueAdapter) Create(key string, value []byte, opts ...interface{}) (uint64, error) {
	return a.kv.Create(key, value)
}

func (a *mockKeyValueAdapter) Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error) {
	return a.kv.Update(key, value, rev)
}

func (a *mockKeyValueAdapter) Get(key string) (Entry, error) {
	mockEntry, err := a.kv.Get(key)
	if err != nil {
		return nil, err
	}
	if mockEntry == nil {
		return nil, nil
	}
	return &mockEntryAdapter{entry: mockEntry}, nil
}

func (a *mockKeyValueAdapter) Delete(key string) error {
	return a.kv.Delete(key)
}

func (a *mockKeyValueAdapter) Watch(key string, opts ...interface{}) (Watcher, error) {
	mockWatcher, err := a.kv.Watch(key)
	if err != nil {
		return nil, err
	}
	return &mockWatcherAdapter{watcher: mockWatcher}, nil
}

// mockEntryAdapter adapts natsmock.Entry to our Entry interface
type mockEntryAdapter struct {
	entry natsmock.Entry
}

func (a *mockEntryAdapter) Key() string {
	return a.entry.Key()
}

func (a *mockEntryAdapter) Value() []byte {
	return a.entry.Value()
}

func (a *mockEntryAdapter) Revision() uint64 {
	return a.entry.Revision()
}

// mockWatcherAdapter adapts natsmock.Watcher to our Watcher interface
type mockWatcherAdapter struct {
	watcher   natsmock.Watcher
	entryChan chan Entry
	once      sync.Once
	initChan  chan struct{}
}

func (a *mockWatcherAdapter) Updates() <-chan Entry {
	// Initialize the channel and goroutine only once
	// This ensures that even if Updates() is called multiple times (which shouldn't happen
	// but could due to race conditions), only one goroutine is created per watcher instance.
	// This matches the behavior of real NATS where Updates() returns the same channel.
	a.once.Do(func() {
		a.entryChan = make(chan Entry, 10)
		a.initChan = make(chan struct{})
		go func() {
			defer close(a.entryChan)
			close(a.initChan) // Signal that goroutine is ready
			for mockEntry := range a.watcher.Updates() {
				if mockEntry != nil {
					a.entryChan <- &mockEntryAdapter{entry: mockEntry}
				} else {
					// nil entry means key was deleted
					a.entryChan <- nil
				}
			}
		}()
		// Wait for goroutine to be ready
		<-a.initChan
	})
	return a.entryChan
}

func (a *mockWatcherAdapter) Stop() {
	a.watcher.Stop()
}

// mockJetStreamAdapter adapts *natsmock.MockJetStream to leader.JetStreamContext interface
type mockJetStreamAdapter struct {
	js *natsmock.MockJetStream
}

func (a *mockJetStreamAdapter) KeyValue(bucket string) (KeyValue, error) {
	kv, err := a.js.KeyValue(bucket)
	if err != nil {
		return nil, err
	}
	return &mockKeyValueAdapter{kv: kv}, nil
}

// mockConnAdapter adapts *natsmock.MockConn to leader.JetStreamProvider interface
type mockConnAdapter struct {
	conn *natsmock.MockConn
}

func newMockConnAdapter(conn *natsmock.MockConn) *mockConnAdapter {
	return &mockConnAdapter{conn: conn}
}

func (a *mockConnAdapter) JetStream() (JetStreamContext, error) {
	js, err := a.conn.JetStream()
	if err != nil {
		return nil, err
	}
	return &mockJetStreamAdapter{js: js}, nil
}
