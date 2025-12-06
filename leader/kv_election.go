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

	return e, nil
}

func (e *kvElection) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx != nil && e.ctx.Err() == nil {
		return ErrAlreadyStarted
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

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

		backoff := baseBackoff * time.Duration(1<<uint(retry))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		jitterAmount := time.Duration(float64(backoff) * jitter * (rand.Float64()*2 - 1))
		finalBackoff := backoff + jitterAmount
		if finalBackoff < 0 {
			finalBackoff = backoff
		}

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

	rev, err := e.kv.Create(e.key, payloadBytes)
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

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.heartbeatLoop(e.ctx)
	}()

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

	e.mu.Unlock()

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
