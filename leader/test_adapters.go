package leader

import (
	"sync"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
)

// MockKeyValueAdapter adapts *natsmock.MockKeyValue to leader.KeyValue interface.
// This adapter enables using natsmock in tests that require the leader.KeyValue interface.
type MockKeyValueAdapter struct {
	KV *natsmock.MockKeyValue
}

// Create creates a key-value entry.
func (a *MockKeyValueAdapter) Create(key string, value []byte, opts ...interface{}) (uint64, error) {
	return a.KV.Create(key, value)
}

// Update updates an existing key-value entry.
func (a *MockKeyValueAdapter) Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error) {
	return a.KV.Update(key, value, rev)
}

// Get retrieves a key-value entry.
func (a *MockKeyValueAdapter) Get(key string) (Entry, error) {
	mockEntry, err := a.KV.Get(key)
	if err != nil {
		return nil, err
	}
	if mockEntry == nil {
		return nil, nil
	}
	return &MockEntryAdapter{Entry: mockEntry}, nil
}

// Delete deletes a key-value entry.
func (a *MockKeyValueAdapter) Delete(key string) error {
	return a.KV.Delete(key)
}

// Watch watches for changes to a key.
func (a *MockKeyValueAdapter) Watch(key string, opts ...interface{}) (Watcher, error) {
	mockWatcher, err := a.KV.Watch(key)
	if err != nil {
		return nil, err
	}
	return NewMockWatcherAdapter(mockWatcher), nil
}

// MockEntryAdapter adapts natsmock.Entry to leader.Entry interface.
type MockEntryAdapter struct {
	Entry natsmock.Entry
}

// Key returns the entry key.
func (a *MockEntryAdapter) Key() string {
	return a.Entry.Key()
}

// Value returns the entry value.
func (a *MockEntryAdapter) Value() []byte {
	return a.Entry.Value()
}

// Revision returns the entry revision.
func (a *MockEntryAdapter) Revision() uint64 {
	return a.Entry.Revision()
}

// MockWatcherAdapter adapts natsmock.Watcher to leader.Watcher interface.
type MockWatcherAdapter struct {
	watcher   natsmock.Watcher
	entryChan chan Entry
	once      sync.Once
	initChan  chan struct{}
}

// NewMockWatcherAdapter creates a new MockWatcherAdapter.
func NewMockWatcherAdapter(watcher natsmock.Watcher) *MockWatcherAdapter {
	return &MockWatcherAdapter{
		watcher:  watcher,
		initChan: make(chan struct{}),
	}
}

// Updates returns a channel that receives entry updates.
// This method initializes the channel and goroutine only once, even if called multiple times.
func (a *MockWatcherAdapter) Updates() <-chan Entry {
	// Initialize the channel and goroutine only once.
	// This ensures that even if Updates() is called multiple times (which shouldn't happen
	// but could due to race conditions), only one goroutine is created per watcher instance.
	// This matches the behavior of real NATS where Updates() returns the same channel.
	a.once.Do(func() {
		a.entryChan = make(chan Entry, 10)
		go func() {
			defer close(a.entryChan)
			close(a.initChan) // Signal that goroutine is ready
			for mockEntry := range a.watcher.Updates() {
				if mockEntry != nil {
					a.entryChan <- &MockEntryAdapter{Entry: mockEntry}
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

// Stop stops the watcher.
func (a *MockWatcherAdapter) Stop() {
	a.watcher.Stop()
}

// MockJetStreamAdapter adapts *natsmock.MockJetStream to leader.JetStreamContext interface.
type MockJetStreamAdapter struct {
	JS *natsmock.MockJetStream
}

// KeyValue returns a KeyValue store for the given bucket.
func (a *MockJetStreamAdapter) KeyValue(bucket string) (KeyValue, error) {
	kv, err := a.JS.KeyValue(bucket)
	if err != nil {
		return nil, err
	}
	return &MockKeyValueAdapter{KV: kv}, nil
}

// MockConnAdapter adapts *natsmock.MockConn to leader.JetStreamProvider interface.
type MockConnAdapter struct {
	Conn *natsmock.MockConn
}

// NewMockConnAdapter creates a new MockConnAdapter.
// This is the main entry point for creating mock adapters in tests.
//
// Example:
//
//	nc := natsmock.NewMockConn()
//	adapter := NewMockConnAdapter(nc)
//	election, err := leader.NewElection(adapter, cfg)
func NewMockConnAdapter(conn *natsmock.MockConn) *MockConnAdapter {
	return &MockConnAdapter{Conn: conn}
}

// JetStream returns a JetStream context.
func (a *MockConnAdapter) JetStream() (JetStreamContext, error) {
	js, err := a.Conn.JetStream()
	if err != nil {
		return nil, err
	}
	return &MockJetStreamAdapter{JS: js}, nil
}

