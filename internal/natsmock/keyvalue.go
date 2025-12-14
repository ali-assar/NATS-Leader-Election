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

type MockEntryImpl struct {
	KeyVal   string
	ValueVal []byte
	RevVal   uint64
}

func (e *MockEntryImpl) Key() string      { return e.KeyVal }
func (e *MockEntryImpl) Value() []byte    { return e.ValueVal }
func (e *MockEntryImpl) Revision() uint64 { return e.RevVal }

// Watcher represents a KeyValue watcher
type Watcher interface {
	Updates() <-chan Entry
	Stop()
}

type MockWatcher struct {
	UpdatesChan chan Entry
	StopChan    chan struct{}
}

func (w *MockWatcher) Updates() <-chan Entry { return w.UpdatesChan }

func (w *MockWatcher) Stop() {
	close(w.StopChan)
}

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

	// Advanced: Delay simulation (deprecated - use channels instead)
	delay time.Duration

	UpdateStartChan    chan struct{}
	UpdateCompleteChan chan struct{}
	UpdateDoneChan     chan struct{}
	CreateStartChan    chan struct{}
	CreateDoneChan     chan struct{}
	WatchStartChan     chan struct{}
}

func NewMockKeyValue() *MockKeyValue {
	return &MockKeyValue{
		data:               make(map[string]*mockEntry),
		rev:                0,
		UpdateStartChan:    make(chan struct{}, 10),
		UpdateCompleteChan: make(chan struct{}, 10),
		UpdateDoneChan:     make(chan struct{}, 10),
		CreateStartChan:    make(chan struct{}, 10),
		CreateDoneChan:     make(chan struct{}, 10),
		WatchStartChan:     make(chan struct{}, 10),
	}
}

func (m *MockKeyValue) Create(key string, value []byte, opts ...KVOption) (uint64, error) {
	select {
	case m.CreateStartChan <- struct{}{}:
	default:
	}

	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Read CreateFunc with lock to avoid race condition
	m.mu.RLock()
	createFunc := m.CreateFunc
	m.mu.RUnlock()

	if createFunc != nil {
		result, err := createFunc(key, value, opts...)
		select {
		case m.CreateDoneChan <- struct{}{}:
		default:
		}
		return result, err
	}

	if m.failCreate {
		select {
		case m.CreateDoneChan <- struct{}{}:
		default:
		}
		return 0, errors.New("create failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		m.data = make(map[string]*mockEntry)
	}

	if _, exists := m.data[key]; exists {
		select {
		case m.CreateDoneChan <- struct{}{}:
		default:
		}
		return 0, errors.New("key already exists")
	}

	m.rev++
	m.data[key] = &mockEntry{
		key:   key,
		Value: value,
		Rev:   m.rev,
	}

	select {
	case m.CreateDoneChan <- struct{}{}:
	default:
	}

	return m.rev, nil
}

func (m *MockKeyValue) Update(key string, value []byte, rev uint64, opts ...KVOption) (uint64, error) {
	select {
	case m.UpdateStartChan <- struct{}{}:
	default:
	}

	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Read UpdateFunc with lock to avoid race condition
	m.mu.RLock()
	updateFunc := m.UpdateFunc
	m.mu.RUnlock()

	if updateFunc != nil {
		result, err := updateFunc(key, value, rev, opts...)
		select {
		case m.UpdateDoneChan <- struct{}{}:
		default:
		}
		return result, err
	}

	if m.failUpdate {
		return 0, errors.New("update failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.data[key]
	if !exists {
		return 0, errors.New("key not found")
	}

	if entry.Rev != rev {
		return 0, errors.New("revision mismatch")
	}

	m.rev++
	entry.Value = value
	entry.Rev = m.rev

	select {
	case m.UpdateDoneChan <- struct{}{}:
	default:
	}

	return m.rev, nil
}

func (m *MockKeyValue) Get(key string) (Entry, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Read GetFunc with lock to avoid race condition
	m.mu.RLock()
	getFunc := m.GetFunc
	m.mu.RUnlock()

	if getFunc != nil {
		return getFunc(key)
	}

	if m.failGet {
		return nil, errors.New("get failed")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, exists := m.data[key]
	if !exists {
		return nil, errors.New("key not found")
	}

	return &MockEntryImpl{
		KeyVal:   key,
		ValueVal: entry.Value,
		RevVal:   entry.Rev,
	}, nil
}

func (m *MockKeyValue) Delete(key string) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Read DeleteFunc with lock to avoid race condition
	m.mu.RLock()
	deleteFunc := m.DeleteFunc
	m.mu.RUnlock()

	if deleteFunc != nil {
		return deleteFunc(key)
	}

	if m.failDelete {
		return errors.New("delete failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.data[key]; !exists {
		return errors.New("key not found")
	}

	delete(m.data, key)

	return nil
}

func (m *MockKeyValue) Watch(key string, opts ...WatchOption) (Watcher, error) {
	select {
	case m.WatchStartChan <- struct{}{}:
	default:
	}

	// Read WatchFunc with lock to avoid race condition
	m.mu.RLock()
	watchFunc := m.WatchFunc
	m.mu.RUnlock()

	if watchFunc != nil {
		return watchFunc(key, opts...)
	}

	if m.failWatch {
		return nil, errors.New("watch failed")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	watcher := &MockWatcher{
		UpdatesChan: make(chan Entry, 1),
		StopChan:    make(chan struct{}),
	}

	if entry, exists := m.data[key]; exists {
		watcher.UpdatesChan <- &MockEntryImpl{
			KeyVal:   key,
			ValueVal: entry.Value,
			RevVal:   entry.Rev,
		}
	}

	close(watcher.UpdatesChan)

	return watcher, nil
}

// SetUpdateFunc safely sets the UpdateFunc field with proper locking
func (m *MockKeyValue) SetUpdateFunc(fn func(key string, value []byte, rev uint64, opts ...KVOption) (uint64, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateFunc = fn
}

// SetCreateFunc safely sets the CreateFunc field with proper locking
func (m *MockKeyValue) SetCreateFunc(fn func(key string, value []byte, opts ...KVOption) (uint64, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CreateFunc = fn
}

// SetGetFunc safely sets the GetFunc field with proper locking
func (m *MockKeyValue) SetGetFunc(fn func(key string) (Entry, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetFunc = fn
}

// SetDeleteFunc safely sets the DeleteFunc field with proper locking
func (m *MockKeyValue) SetDeleteFunc(fn func(key string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteFunc = fn
}

// SetWatchFunc safely sets the WatchFunc field with proper locking
func (m *MockKeyValue) SetWatchFunc(fn func(key string, opts ...WatchOption) (Watcher, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WatchFunc = fn
}
