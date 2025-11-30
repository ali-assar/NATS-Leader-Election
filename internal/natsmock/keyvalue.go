package natsmock

import (
	"errors"
	"sync"
	"time"
)

// KVOption represents NATS KV options (simplified for mock)
type KVOption interface{}

// WatchOption represents NATS watch options (simplified for mock)
type WatchOption interface{}

// Entry represents a KeyValue entry
type Entry interface {
	Key() string
	Value() []byte
	Revision() uint64
}

// mockEntry is our internal entry representation
type mockEntry struct {
	key   string
	Value []byte
	Rev   uint64
}

// mockEntryImpl implements Entry interface
type mockEntryImpl struct {
	key   string
	value []byte
	rev   uint64
}

func (e *mockEntryImpl) Key() string      { return e.key }
func (e *mockEntryImpl) Value() []byte    { return e.value }
func (e *mockEntryImpl) Revision() uint64 { return e.rev }

// Watcher represents a KeyValue watcher
type Watcher interface {
	Updates() <-chan Entry
	Stop()
}

// mockWatcher is a simple watcher implementation
type mockWatcher struct {
	updates chan Entry
	stop    chan struct{}
}

func (w *mockWatcher) Updates() <-chan Entry { return w.updates }
func (w *mockWatcher) Stop()                 { close(w.stop) }

type MockKeyValue struct {
	data map[string]*mockEntry
	mu   sync.RWMutex
	rev  uint64 // Revision counter

	// Failure flags
	failCreate bool
	failUpdate bool
	failGet    bool
	failDelete bool
	failWatch  bool

	// Custom function overrides
	CreateFunc func(key string, value []byte, opts ...KVOption) (uint64, error)
	UpdateFunc func(key string, value []byte, rev uint64, opts ...KVOption) (uint64, error)
	GetFunc    func(key string) (Entry, error)
	DeleteFunc func(key string) error
	WatchFunc  func(key string, opts ...WatchOption) (Watcher, error)

	// Advanced: Delay simulation
	delay time.Duration
}

// NewMockKeyValue creates a new MockKeyValue instance
func NewMockKeyValue() *MockKeyValue {
	return &MockKeyValue{
		data: make(map[string]*mockEntry),
		rev:  0,
	}
}

func (m *MockKeyValue) Create(key string, value []byte, opts ...KVOption) (uint64, error) {
	// Apply delay if set
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Custom function takes priority
	if m.CreateFunc != nil {
		return m.CreateFunc(key, value, opts...)
	}

	// Simple flag check
	if m.failCreate {
		return 0, errors.New("create failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize map if needed (defensive)
	if m.data == nil {
		m.data = make(map[string]*mockEntry)
	}

	// Check if key already exists (NATS behavior)
	if _, exists := m.data[key]; exists {
		return 0, errors.New("key already exists")
	}

	// Increment revision and create entry
	m.rev++
	m.data[key] = &mockEntry{
		key:   key,
		Value: value,
		Rev:   m.rev,
	}

	return m.rev, nil
}

func (m *MockKeyValue) Update(key string, value []byte, rev uint64, opts ...KVOption) (uint64, error) {
	// Apply delay if set
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Custom function takes priority
	if m.UpdateFunc != nil {
		return m.UpdateFunc(key, value, rev, opts...)
	}

	// Simple flag check
	if m.failUpdate {
		return 0, errors.New("update failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if key exists
	entry, exists := m.data[key]
	if !exists {
		return 0, errors.New("key not found")
	}

	// Check revision matches (optimistic concurrency)
	if entry.Rev != rev {
		return 0, errors.New("revision mismatch")
	}

	// Update entry and increment revision
	m.rev++
	entry.Value = value
	entry.Rev = m.rev

	return m.rev, nil
}

func (m *MockKeyValue) Get(key string) (Entry, error) {
	// Apply delay if set
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Custom function takes priority
	if m.GetFunc != nil {
		return m.GetFunc(key)
	}

	// Simple flag check
	if m.failGet {
		return nil, errors.New("get failed")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check if key exists
	entry, exists := m.data[key]
	if !exists {
		return nil, errors.New("key not found")
	}

	// Return Entry interface implementation
	return &mockEntryImpl{
		key:   key,
		value: entry.Value,
		rev:   entry.Rev,
	}, nil
}

func (m *MockKeyValue) Delete(key string) error {
	// Apply delay if set
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Custom function takes priority
	if m.DeleteFunc != nil {
		return m.DeleteFunc(key)
	}

	// Simple flag check
	if m.failDelete {
		return errors.New("delete failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete the key
	delete(m.data, key)

	return nil
}

func (m *MockKeyValue) Watch(key string, opts ...WatchOption) (Watcher, error) {
	// Custom function takes priority
	if m.WatchFunc != nil {
		return m.WatchFunc(key, opts...)
	}

	// Simple flag check
	if m.failWatch {
		return nil, errors.New("watch failed")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a simple watcher that sends current state
	watcher := &mockWatcher{
		updates: make(chan Entry, 1),
		stop:    make(chan struct{}),
	}

	// If key exists, send current entry
	if entry, exists := m.data[key]; exists {
		watcher.updates <- &mockEntryImpl{
			key:   key,
			value: entry.Value,
			rev:   entry.Rev,
		}
	}

	// Close channel to indicate no more updates for now
	close(watcher.updates)

	return watcher, nil
}
