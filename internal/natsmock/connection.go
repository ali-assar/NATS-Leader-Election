package natsmock

import (
	"errors"
	"sync"
)

type Status int

const (
	StatusConnected Status = iota
	StatusDisconnected
	StatusClosed
)

type MockJetStream struct {
	buckets map[string]*MockKeyValue
	mu      sync.RWMutex
}

func NewMockJetStream() *MockJetStream {
	return &MockJetStream{
		buckets: make(map[string]*MockKeyValue),
	}
}

func (m *MockJetStream) KeyValue(bucket string) (*MockKeyValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if kv, exists := m.buckets[bucket]; exists {
		return kv, nil
	}
	kv := NewMockKeyValue()
	m.buckets[bucket] = kv
	return kv, nil
}

type MockConn struct {
	status        Status
	js            *MockJetStream
	failJetStream bool
}

func NewMockConn() *MockConn {
	return &MockConn{
		status: StatusConnected,
		js:     NewMockJetStream(),
	}
}

func (m *MockConn) JetStream() (*MockJetStream, error) {
	if m.failJetStream {
		return nil, errors.New("jetstream failed")
	}

	if m.status != StatusConnected {
		return nil, errors.New("connection not available")
	}

	return m.js, nil
}

func (m *MockConn) Status() Status {
	return m.status
}

func (m *MockConn) SetStatus(status Status) {
	m.status = status
}

func (m *MockConn) Disconnect() {
	m.status = StatusDisconnected
}

func (m *MockConn) Close() {
	m.status = StatusClosed
}
