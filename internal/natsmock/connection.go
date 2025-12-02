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

// MockJetStream simulates NATS JetStream
// It stores multiple KeyValue buckets
type MockJetStream struct {
	buckets map[string]*MockKeyValue
	mu      sync.RWMutex
}

// NewMockJetStream creates a new MockJetStream
func NewMockJetStream() *MockJetStream {
	return &MockJetStream{
		buckets: make(map[string]*MockKeyValue),
	}
}

// KeyValue returns a MockKeyValue for the given bucket name
// If the bucket doesn't exist, it creates a new one
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

// MockConn simulates a NATS connection
// It provides access to JetStream and tracks connection status
type MockConn struct {
	status        Status
	js            *MockJetStream
	failJetStream bool
}

// NewMockConn creates a new MockConn with connected status
func NewMockConn() *MockConn {
	return &MockConn{
		status: StatusConnected,
		js:     NewMockJetStream(),
	}
}

// JetStream returns the MockJetStream instance
// This simulates: nc.JetStream() from real NATS
func (m *MockConn) JetStream() (*MockJetStream, error) {
	// Check if we should fail
	if m.failJetStream {
		return nil, errors.New("jetstream failed")
	}

	// Check if connection is valid
	if m.status != StatusConnected {
		return nil, errors.New("connection not available")
	}

	return m.js, nil
}

// Status returns the current connection status
func (m *MockConn) Status() Status {
	return m.status
}

// SetStatus allows you to change connection status (for testing)
// Example: mockConn.SetStatus(StatusDisconnected) to simulate disconnection
func (m *MockConn) SetStatus(status Status) {
	m.status = status
}

// Disconnect simulates a disconnection
func (m *MockConn) Disconnect() {
	m.status = StatusDisconnected
}

// Close simulates closing the connection
func (m *MockConn) Close() {
	m.status = StatusClosed
}
