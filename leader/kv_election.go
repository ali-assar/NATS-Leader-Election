package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Election state constants
const (
	StateInit      = "INIT"
	StateCandidate = "CANDIDATE"
	StateLeader    = "LEADER"
	StateFollower  = "FOLLOWER"
	StateDemoted   = "DEMOTED"
	StateStopped   = "STOPPED"
)

const (
	jitterMin   = 10 * time.Millisecond
	jitterMax   = 100 * time.Millisecond
	maxRetries  = 3
	baseBackoff = 50 * time.Millisecond
	maxBackoff  = 5 * time.Second
	jitter      = 0.1
)

type kvElection struct {
	cfg ElectionConfig
	nc  JetStreamProvider
	kv  KeyValue
	key string

	isLeader atomic.Bool
	leaderID atomic.Value
	token    atomic.Value
	revision atomic.Uint64
	state    atomic.Value
	mu       sync.RWMutex

	lastHeartbeat  atomic.Value
	lastTransition atomic.Value

	watcherRunning atomic.Bool

	wg sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc

	onPromote func(ctx context.Context, token string)
	onDemote  func()

	// Connection monitoring
	connectionMonitor ConnectionMonitor
	disconnectHandler *disconnectHandler

	healthFailureCount atomic.Int32
}

func newKVElection(nc JetStreamProvider, cfg ElectionConfig) (*kvElection, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream: %w", err)
	}

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
	e.state.Store(StateInit)
	e.lastHeartbeat.Store(time.Time{})
	e.lastTransition.Store(time.Time{})

	// Initialize connection monitor if NATS connection is available
	if connProvider, ok := nc.(NATSConnectionProvider); ok {
		if conn := connProvider.NATSConnection(); conn != nil {
			e.connectionMonitor = NewNATSConnectionMonitor(conn)
			e.disconnectHandler = &disconnectHandler{
				election: e,
			}
		}
	}

	return e, nil
}

func (e *kvElection) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx != nil && e.ctx.Err() == nil {
		return ErrAlreadyStarted
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

	// Start connection monitoring if available
	if e.connectionMonitor != nil {
		if err := e.connectionMonitor.Start(ctx); err != nil {
			e.cancel()
			return fmt.Errorf("failed to start connection monitor: %w", err)
		}

		// Register disconnect and reconnect handlers
		e.connectionMonitor.OnDisconnect(e.disconnectHandler.handleDisconnect)
		e.connectionMonitor.OnReconnect(e.handleReconnect)
	}

	e.state.Store(StateCandidate)
	e.lastTransition.Store(time.Now())

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := e.attemptAcquire(); err != nil {
			e.becomeFollower()
		}
	}()

	return nil
}

// attemptAcquireWithRetry attempts to acquire leadership with jitter and exponential backoff.
func (e *kvElection) attemptAcquireWithRetry(ctx context.Context) {
	jitterRange := jitterMax - jitterMin
	initialJitter := jitterMin + time.Duration(rand.Float64()*float64(jitterRange))

	select {
	case <-ctx.Done():
		return
	case <-time.After(initialJitter):
	}

	for retry := 0; retry <= maxRetries; retry++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := e.attemptAcquire()
		if err == nil {
			return
		}

		if retry == maxRetries {
			e.becomeFollower()
			return
		}

		finalBackoff := CalculateBackoff(DefaultBackoffConfig(), retry)

		select {
		case <-ctx.Done():
			return
		case <-time.After(finalBackoff):
		}
	}
}

func (e *kvElection) attemptAcquire() error {
	token := uuid.New().String()

	payload := map[string]interface{}{
		"id":    e.cfg.InstanceID,
		"token": token,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Use TTL if configured (NATS KV uses nats.WithMaxAge)
	var opts []interface{}
	if e.cfg.TTL > 0 {
		opts = append(opts, e.cfg.TTL)
	}

	rev, err := e.kv.Create(e.key, payloadBytes, opts...)
	if err != nil {
		return err
	}

	e.becomeLeader(token, rev)
	return nil
}

func (e *kvElection) becomeLeader(token string, rev uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isLeader.Store(true)
	e.leaderID.Store(e.cfg.InstanceID)
	e.token.Store(token)
	e.revision.Store(rev)
	e.state.Store(StateLeader)
	e.lastHeartbeat.Store(time.Now())
	e.lastTransition.Store(time.Now())

	// Start heartbeat loop (always)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.heartbeatLoop(e.ctx)
	}()

	// Start validation loop (always)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.validationLoop(e.ctx)
	}()

	// Call onPromote callback (if set)
	if e.onPromote != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					// Panic occurred in user callback - log it when logger is integrated
				}
			}()
			e.onPromote(e.ctx, token)
		}()
	}
}

func (e *kvElection) becomeFollower() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isLeader.Store(false)
	e.state.Store(StateFollower)
	e.lastTransition.Store(time.Now())

	if e.ctx != nil && !e.watcherRunning.Load() {
		e.watcherRunning.Store(true)
		e.wg.Add(1)
		go func() {
			defer e.watcherRunning.Store(false)
			defer e.wg.Done()
			e.watchLoop(e.ctx)
		}()
	}
}

func (e *kvElection) Stop() error {
	e.mu.Lock()

	if e.ctx == nil {
		e.mu.Unlock()
		return ErrAlreadyStopped
	}

	wasLeader := e.isLeader.Load()

	if e.cancel != nil {
		e.cancel()
	}

	e.isLeader.Store(false)
	e.state.Store(StateStopped)
	e.lastTransition.Store(time.Now())
	e.watcherRunning.Store(false)

	// Stop disconnect handler timer
	if e.disconnectHandler != nil {
		e.disconnectHandler.stop()
	}

	e.mu.Unlock()

	// Stop connection monitoring
	if e.connectionMonitor != nil {
		_ = e.connectionMonitor.Stop()
	}

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}

	if wasLeader && e.onDemote != nil {
		e.onDemote()
	}

	return nil
}

func (e *kvElection) StopWithContext(ctx context.Context) error {
	return e.Stop()
}

func (e *kvElection) Status() ElectionStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	state := StateInit
	if s := e.state.Load(); s != nil {
		if str, ok := s.(string); ok {
			state = str
		}
	}

	leaderID := ""
	if id := e.leaderID.Load(); id != nil {
		if str, ok := id.(string); ok {
			leaderID = str
		}
	}

	token := ""
	if t := e.token.Load(); t != nil {
		if str, ok := t.(string); ok {
			token = str
		}
	}

	lastHeartbeat := time.Time{}
	if hb := e.lastHeartbeat.Load(); hb != nil {
		if t, ok := hb.(time.Time); ok {
			lastHeartbeat = t
		}
	}

	lastTransition := time.Time{}
	if lt := e.lastTransition.Load(); lt != nil {
		if t, ok := lt.(time.Time); ok {
			lastTransition = t
		}
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

func (e *kvElection) IsLeader() bool {
	return e.isLeader.Load()
}

func (e *kvElection) LeaderID() string {
	if id := e.leaderID.Load(); id != nil {
		return id.(string)
	}
	return ""
}

func (e *kvElection) Token() string {
	if t := e.token.Load(); t != nil {
		return t.(string)
	}
	return ""
}

func (e *kvElection) OnPromote(fn func(ctx context.Context, token string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onPromote = fn
}

func (e *kvElection) OnDemote(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onDemote = fn
}

// validateToken checks if the current local token matches the token in KV store.
// If error occurs, returns false (fail-safe: assume invalid).
func (e *kvElection) validateToken(ctx context.Context) (bool, error) {
	localToken := e.Token()
	if localToken == "" {
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "no token",
			Err:        nil,
		}
	}

	// Check context before making the call
	select {
	case <-ctx.Done():
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "validation timeout",
			Err:        ctx.Err(),
		}
	default:
	}

	// Wrap Get() in a goroutine to respect context timeout
	type getResult struct {
		entry Entry
		err   error
	}
	resultChan := make(chan getResult, 1)

	go func() {
		entry, err := e.kv.Get(e.key)
		resultChan <- getResult{entry: entry, err: err}
	}()

	var entry Entry
	var err error
	select {
	case <-ctx.Done():
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "validation timeout",
			Err:        ctx.Err(),
		}
	case result := <-resultChan:
		entry = result.entry
		err = result.err
	}

	if err != nil {
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "failed to get token from KV",
			Err:        err,
		}
	}

	if entry == nil {
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "no token in KV",
			Err:        nil,
		}
	}

	payload := make(map[string]interface{})
	if err := json.Unmarshal(entry.Value(), &payload); err != nil {
		return false, err
	}

	kvTokenInterface, ok := payload["token"]
	if !ok {
		return false, &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "no token in KV",
			Err:        nil,
		}
	}

	kvToken, ok := kvTokenInterface.(string)
	if !ok {
		return false, &TokenValidationError{
			LocalToken: localToken,
			KvToken:    "",
			LeaderID:   e.cfg.InstanceID,
			Reason:     "invalid token type",
			Err:        nil,
		}
	}

	if localToken != kvToken {
		return false, &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "token mismatch",
			Err:        nil,
		}
	}

	leaderIDInterface, ok := payload["id"]
	if !ok {
		return false, &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "missing leader ID in payload",
			Err:        nil,
		}
	}

	leaderID, ok := leaderIDInterface.(string)
	if !ok {
		return false, &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "invalid leader ID type",
			Err:        nil,
		}
	}

	if leaderID != e.cfg.InstanceID {
		return false, &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "leader ID mismatch",
			Err:        nil,
		}
	}

	return true, nil
}

// ValidateToken validates the current fencing token against the KV store.
// This method can be called before critical operations to ensure the leader is still valid.
func (e *kvElection) ValidateToken(ctx context.Context) (bool, error) {
	if !e.IsLeader() {
		return false, ErrNotLeader
	}

	return e.validateToken(ctx)
}

// ValidateTokenOrDemote validates the token and demotes if invalid.
// Returns true if valid, false if invalid (and demoted).
// Use this for operations that must not proceed with an invalid token.
func (e *kvElection) ValidateTokenOrDemote(ctx context.Context) bool {
	isValid, err := e.ValidateToken(ctx)
	if err != nil || !isValid {
		if e.IsLeader() {
			e.handleValidationFailure(err)
		}
		return false
	}
	return true
}
