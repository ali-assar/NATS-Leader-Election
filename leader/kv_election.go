package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// kvElection is the internal implementation of Election interface.
type kvElection struct {
	cfg ElectionConfig
	nc  JetStreamProvider
	kv  KeyValue
	key string // Full key path: cfg.Group

	// State (thread-safe)
	isLeader atomic.Bool
	leaderID atomic.Value // string
	token    atomic.Value // string
	revision atomic.Uint64
	state    atomic.Value // string
	mu       sync.RWMutex // Protects non-atomic fields

	// Timing
	lastHeartbeat  atomic.Value // time.Time
	lastTransition atomic.Value // time.Time

	// Context management (set in Start(), not constructor)
	ctx    context.Context
	cancel context.CancelFunc

	// Callbacks
	onPromote func(ctx context.Context, token string)
	onDemote  func()
}

func newKVElection(nc JetStreamProvider, cfg ElectionConfig) (*kvElection, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream: %w", err)
	}

	// Get existing KV bucket (don't create - bucket should already exist)
	// TODO: In Phase 2, add BucketAutoCreate option
	kv, err := js.KeyValue(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get KV bucket %s: %w", cfg.Bucket, err)
	}

	e := &kvElection{
		cfg: cfg,
		nc:  nc,
		kv:  kv,
		key: cfg.Group,
	}

	e.isLeader.Store(false)
	e.leaderID.Store("")
	e.token.Store("")
	e.revision.Store(0)
	e.state.Store("INIT")
	e.lastHeartbeat.Store(time.Time{})
	e.lastTransition.Store(time.Time{})

	return e, nil
}

func (e *kvElection) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx != nil && e.ctx.Err() == nil {
		return ErrAlreadyStarted
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

	e.state.Store("CANDIDATE")
	e.lastTransition.Store(time.Now())

	go e.attemptAcquire()

	return nil
}

// attemptAcquire tries to acquire leadership by creating the key.
// This is called in a goroutine from Start().
func (e *kvElection) attemptAcquire() {
	token := uuid.New().String()

	payload := map[string]interface{}{
		"id":    e.cfg.InstanceID,
		"token": token,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	// TODO: Add TTL support (nats.WithMaxAge) in next step
	rev, err := e.kv.Create(e.key, payloadBytes)
	if err != nil {
		e.becomeFollower()
		return
	}

	e.becomeLeader(token, rev)
}

func (e *kvElection) becomeLeader(token string, rev uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isLeader.Store(true)
	e.leaderID.Store(e.cfg.InstanceID)
	e.token.Store(token)
	e.revision.Store(rev)
	e.state.Store("LEADER")
	e.lastHeartbeat.Store(time.Now())
	e.lastTransition.Store(time.Now())

	// TODO: Start heartbeat loop (Step 4)
	// go e.heartbeatLoop()

	if e.onPromote != nil {
		go e.onPromote(e.ctx, token)
	}
}

func (e *kvElection) becomeFollower() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isLeader.Store(false)
	e.state.Store("FOLLOWER")
	e.lastTransition.Store(time.Now())

	// TODO: Start watcher to monitor for leader changes (Step 5)
	// go e.watchLoop()
}

// Stop stops the election
func (e *kvElection) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx == nil {
		return ErrAlreadyStopped
	}

	if e.cancel != nil {
		e.cancel()
	}

	e.isLeader.Store(false)
	e.state.Store("STOPPED")
	e.lastTransition.Store(time.Now())

	if e.onDemote != nil && e.isLeader.Load() {
		e.onDemote()
	}

	return nil
}

// StopWithContext stops the election with a context
func (e *kvElection) StopWithContext(ctx context.Context) error {
	return e.Stop() // TODO: Implement timeout logic in Phase 2
}

// Status returns the current election status
func (e *kvElection) Status() ElectionStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	state := "INIT"
	if s := e.state.Load(); s != nil {
		state = s.(string)
	}

	leaderID := ""
	if id := e.leaderID.Load(); id != nil {
		leaderID = id.(string)
	}

	token := ""
	if t := e.token.Load(); t != nil {
		token = t.(string)
	}

	lastHeartbeat := time.Time{}
	if hb := e.lastHeartbeat.Load(); hb != nil {
		lastHeartbeat = hb.(time.Time)
	}

	lastTransition := time.Time{}
	if lt := e.lastTransition.Load(); lt != nil {
		lastTransition = lt.(time.Time)
	}

	return ElectionStatus{
		State:          state,
		IsLeader:       e.isLeader.Load(),
		LeaderID:       leaderID,
		Token:          token,
		LastHeartbeat:  lastHeartbeat,
		LastTransition: lastTransition,
		Revision:       e.revision.Load(),
	}
}

// IsLeader returns true if this instance is the leader
func (e *kvElection) IsLeader() bool {
	return e.isLeader.Load()
}

// LeaderID returns the current leader's instance ID
func (e *kvElection) LeaderID() string {
	if id := e.leaderID.Load(); id != nil {
		return id.(string)
	}
	return ""
}

// Token returns the current fencing token if leader
func (e *kvElection) Token() string {
	if t := e.token.Load(); t != nil {
		return t.(string)
	}
	return ""
}

// OnPromote registers a callback for when this instance becomes leader
func (e *kvElection) OnPromote(fn func(ctx context.Context, token string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onPromote = fn
}

// OnDemote registers a callback for when this instance loses leadership
func (e *kvElection) OnDemote(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onDemote = fn
}
