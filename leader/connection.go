package leader

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

type ConnectionStatus int

const (
	ConnectionStatusConnected ConnectionStatus = iota
	ConnectionStatusDisconnected
	ConnectionStatusReconnected
	ConnectionStatusClosed
)

type ConnectionMonitor interface {
	Start(ctx context.Context) error
	Stop() error
	Status() ConnectionStatus
	OnDisconnect(func())
	OnReconnect(func())
	SetStatus(ConnectionStatus) // Set status (used after verification)
}

type natsConnectionMonitor struct {
	conn              *nats.Conn
	status            atomic.Value
	mu                sync.RWMutex
	disconnectHandler func()
	reconnectHandler  func()
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
}

func NewNATSConnectionMonitor(conn *nats.Conn) ConnectionMonitor {
	return &natsConnectionMonitor{
		conn: conn,
	}
}

func (m *natsConnectionMonitor) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx != nil {
		return ErrAlreadyStarted
	}

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.status.Store(ConnectionStatusConnected)

	m.conn.SetDisconnectHandler(m.handleDisconnect)
	m.conn.SetReconnectHandler(m.handleReconnect)
	m.conn.SetClosedHandler(m.handleClosed)

	return nil
}

func (m *natsConnectionMonitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel == nil {
		return ErrAlreadyStopped
	}

	m.cancel()
	m.wg.Wait()

	m.conn.SetDisconnectHandler(nil)
	m.conn.SetReconnectHandler(nil)
	m.conn.SetClosedHandler(nil)

	m.status.Store(ConnectionStatusDisconnected)

	return nil
}

func (m *natsConnectionMonitor) Status() ConnectionStatus {
	if s := m.status.Load(); s != nil {
		return s.(ConnectionStatus)
	}
	return ConnectionStatusDisconnected
}

func (m *natsConnectionMonitor) OnDisconnect(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnectHandler = fn
}

func (m *natsConnectionMonitor) OnReconnect(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnectHandler = fn
}

func (m *natsConnectionMonitor) SetStatus(status ConnectionStatus) {
	m.status.Store(status)
}

func (m *natsConnectionMonitor) handleDisconnect(nc *nats.Conn) {
	m.status.Store(ConnectionStatusDisconnected)

	m.mu.RLock()
	handler := m.disconnectHandler
	m.mu.RUnlock()

	if handler != nil {
		handler()
	}
}

func (m *natsConnectionMonitor) handleReconnect(nc *nats.Conn) {
	m.status.Store(ConnectionStatusReconnected)

	m.mu.RLock()
	handler := m.reconnectHandler
	m.mu.RUnlock()

	if handler != nil {
		handler()
	}
}

func (m *natsConnectionMonitor) handleClosed(nc *nats.Conn) {
	m.status.Store(ConnectionStatusClosed)
}

// disconnectHandler manages disconnect grace period for an election
type disconnectHandler struct {
	election       *kvElection
	gracePeriod    time.Duration
	timer          *time.Timer
	mu             sync.Mutex
	disconnectedAt time.Time
}

func (d *disconnectHandler) handleDisconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Only handle if we're the leader
	if !d.election.isLeader.Load() {
		return
	}

	// Calculate grace period
	gracePeriod := d.election.cfg.DisconnectGracePeriod
	if gracePeriod == 0 {
		gracePeriod = 3 * d.election.cfg.HeartbeatInterval
		if gracePeriod < 5*time.Second {
			gracePeriod = 5 * time.Second
		}
	}

	// Stop existing timer if any
	if d.timer != nil {
		d.timer.Stop()
	}

	// Record disconnect time
	d.disconnectedAt = time.Now()

	// Start grace period timer
	d.timer = time.AfterFunc(gracePeriod, func() {
		d.handleGracePeriodExpired()
	})
}

// handleGracePeriodExpired is called when grace period expires
func (d *disconnectHandler) handleGracePeriodExpired() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.election.connectionMonitor != nil {
		if d.election.connectionMonitor.Status() != ConnectionStatusDisconnected {
			// Reconnected, don't demote
			return
		}
	}

	// Still disconnected, demote if still leader
	if d.election.isLeader.Load() {
		d.election.becomeFollower()

		d.election.mu.RLock()
		onDemote := d.election.onDemote
		d.election.mu.RUnlock()

		if onDemote != nil {
			onDemote()
		}
	}
}

// stop stops the grace period timer
func (d *disconnectHandler) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func (e *kvElection) handleReconnect() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.disconnectHandler.timer != nil {
		e.disconnectHandler.timer.Stop()
		e.disconnectHandler.timer = nil
	}

	if !e.isLeader.Load() {
		return
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.verifyLeadershipAfterReconnect()
	}()
}

func (e *kvElection) verifyLeadershipAfterReconnect() {
	// Give connection a moment to stabilize
	time.Sleep(100 * time.Millisecond)

	// Verify connection is working
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Test connection with a simple operation
	_, err := e.kv.Get(e.key)
	if err != nil {
		// Connection not working, demote
		e.handleReconnectVerificationFailed(err)
		return
	}

	// Verify token is still valid
	isValid, err := e.validateToken(ctx)
	if err != nil || !isValid {
		// Token invalid, demote
		e.handleReconnectVerificationFailed(err)
		return
	}

	// Verification passed, resume operations
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if still leader (might have been demoted during verification)
	if !e.isLeader.Load() {
		return
	}

	// Resume heartbeat loop if it was stopped
	// Note: Heartbeat loop should resume automatically, but we verify
	// Update status to Connected after successful verification
	if e.connectionMonitor != nil {
		e.connectionMonitor.SetStatus(ConnectionStatusConnected)
	}
}

func (e *kvElection) handleReconnectVerificationFailed(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.isLeader.Load() {
		e.becomeFollower()

		e.mu.RLock()
		onDemote := e.onDemote
		e.mu.RUnlock()

		if onDemote != nil {
			onDemote()
		}
	}
}
