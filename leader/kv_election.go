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
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
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

	lastHeartbeat   atomic.Value
	lastTransition  atomic.Value
	leaderStartTime atomic.Value // Track when leadership started for duration metric

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

// leadershipPayload represents the value stored in the leadership key
type leadershipPayload struct {
	ID       string `json:"id"`
	Token    string `json:"token"`
	Priority int    `json:"priority,omitempty"` // Omit if 0 for backward compatibility
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
	e.leaderStartTime.Store(time.Time{})

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

// getMetricsLabels returns the standard Prometheus labels for metrics
func (e *kvElection) getMetricsLabels() prometheus.Labels {
	return prometheus.Labels{
		"role":        e.cfg.Group,
		"instance_id": e.cfg.InstanceID,
		"bucket":      e.cfg.Bucket,
	}
}

// recordTransition records a state transition metric
func (e *kvElection) recordTransition(fromState, toState string) {
	if e.cfg.Metrics == nil {
		return
	}
	labels := e.getMetricsLabels()
	labels["from_state"] = fromState
	labels["to_state"] = toState
	e.cfg.Metrics.IncTransitions(labels)
}

// recordFailure records a failure metric
func (e *kvElection) recordFailure(errorType string) {
	if e.cfg.Metrics == nil {
		return
	}
	labels := e.getMetricsLabels()
	labels["error_type"] = errorType
	e.cfg.Metrics.IncFailures(labels)
}

// recordAcquireAttempt records an acquisition attempt metric
func (e *kvElection) recordAcquireAttempt(status string) {
	if e.cfg.Metrics == nil {
		return
	}
	labels := e.getMetricsLabels()
	labels["status"] = status
	e.cfg.Metrics.IncAcquireAttempts(labels)
}

// updateIsLeaderMetric updates the is_leader gauge metric
func (e *kvElection) updateIsLeaderMetric() {
	if e.cfg.Metrics == nil {
		return
	}
	value := float64(0)
	if e.isLeader.Load() {
		value = 1
	}
	e.cfg.Metrics.SetIsLeader(value, e.getMetricsLabels())
}

// recordLeaderDuration records how long leadership was held
func (e *kvElection) recordLeaderDuration() {
	if e.cfg.Metrics == nil {
		return
	}
	startTime := e.leaderStartTime.Load()
	if startTime == nil {
		return
	}
	if t, ok := startTime.(time.Time); ok && !t.IsZero() {
		duration := time.Since(t)
		e.cfg.Metrics.ObserveLeaderDuration(duration, e.getMetricsLabels())
	}
}

func (e *kvElection) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ctx != nil && e.ctx.Err() == nil {
		return ErrAlreadyStarted
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

	if e.connectionMonitor != nil {
		if err := e.connectionMonitor.Start(ctx); err != nil {
			e.cancel()
			return fmt.Errorf("failed to start connection monitor: %w", err)
		}

		e.connectionMonitor.OnDisconnect(e.disconnectHandler.handleDisconnect)
		e.connectionMonitor.OnReconnect(e.handleReconnect)

		if e.cfg.Metrics != nil {
			e.cfg.Metrics.SetConnectionStatus(1, e.getMetricsLabels())
		}
	}

	e.state.Store(StateCandidate)
	e.lastTransition.Store(time.Now())

	log := e.getLogger()
	log.Info("election_started",
		append(e.logWithContext(ctx),
			zap.String("state", StateCandidate),
			zap.Duration("ttl", e.cfg.TTL),
			zap.Duration("heartbeat_interval", e.cfg.HeartbeatInterval),
		)...,
	)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := e.attemptAcquire(); err != nil {
			e.recordAcquireAttempt("failed")
			e.recordFailure(classifyErrorType(err))
			e.becomeFollower()
		}
	}()

	return nil
}

// attemptAcquireWithRetry attempts to acquire leadership with initial jitter and exponential backoff.
// The jitter prevents thundering herd problems when multiple followers detect leader failure simultaneously.
func (e *kvElection) attemptAcquireWithRetry(ctx context.Context) {
	jitterRange := jitterMax - jitterMin
	initialJitter := jitterMin + time.Duration(rand.Float64()*float64(jitterRange))

	log := e.getLogger()
	log.Debug("attempting_acquire_with_retry",
		append(e.logWithContext(ctx),
			zap.Duration("initial_jitter", initialJitter),
		)...,
	)

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

		// Record failed acquire attempt (attemptAcquire already records success)
		e.recordAcquireAttempt("failed")
		e.recordFailure(classifyErrorType(err))

		if retry == maxRetries {
			log.Warn("acquire_failed_max_retries",
				append(e.logWithContext(ctx),
					zap.Int("max_retries", maxRetries),
					zap.Error(err),
				)...,
			)
			e.becomeFollower()
			return
		}

		finalBackoff := CalculateBackoff(DefaultBackoffConfig(), retry)
		log.Debug("acquire_retry",
			append(e.logWithContext(ctx),
				zap.Int("retry", retry),
				zap.Duration("backoff", finalBackoff),
				zap.Error(err),
			)...,
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(finalBackoff):
		}
	}
}

func (e *kvElection) attemptAcquire() error {
	token := uuid.New().String()

	payload := leadershipPayload{
		ID:       e.cfg.InstanceID,
		Token:    token,
		Priority: e.cfg.Priority,
	}
	payloadBytes, err := json.Marshal(payload)

	if err != nil {
		log := e.getLogger()
		log.Error("acquire_failed",
			append(e.logWithContext(e.ctx),
				zap.Error(err),
				zap.String("error_type", "marshal_error"),
			)...,
		)
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var opts []interface{}
	if e.cfg.TTL > 0 {
		opts = append(opts, e.cfg.TTL)
	}

	rev, err := e.kv.Create(e.key, payloadBytes, opts...)
	if err != nil {
		// Key exists - check if we should attempt priority takeover
		if e.cfg.AllowPriorityTakeover && e.cfg.Priority > 0 {
			return e.attemptPriorityTakeover(payloadBytes)
		}

		log := e.getLogger()
		log.Debug("acquire_failed",
			append(e.logWithContext(e.ctx),
				zap.Error(err),
				zap.String("error_type", classifyErrorType(err)),
			)...,
		)
		e.recordAcquireAttempt("failed")
		e.recordFailure(classifyErrorType(err))
		return err
	}

	log := e.getLogger()
	log.Info("acquire_success",
		append(e.logWithContext(e.ctx),
			zap.String("token", token),
			zap.Uint64("revision", rev),
		)...,
	)

	e.recordAcquireAttempt("success")
	e.becomeLeader(token, rev)
	return nil
}

func (e *kvElection) becomeLeader(token string, rev uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	fromState := StateInit
	if s := e.state.Load(); s != nil {
		if str, ok := s.(string); ok {
			fromState = str
		}
	}

	e.isLeader.Store(true)
	e.leaderID.Store(e.cfg.InstanceID)
	e.token.Store(token)
	e.revision.Store(rev)
	e.state.Store(StateLeader)
	now := time.Now()
	e.lastHeartbeat.Store(now)
	e.lastTransition.Store(now)
	e.leaderStartTime.Store(now)

	e.recordTransition(fromState, StateLeader)
	e.updateIsLeaderMetric()

	log := e.getLogger()
	log.Info("state_transition",
		append(e.logWithContext(e.ctx),
			zap.String("from_state", fromState),
			zap.String("to_state", StateLeader),
			zap.String("token", token),
			zap.Uint64("revision", rev),
		)...,
	)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.heartbeatLoop(e.ctx)
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.validationLoop(e.ctx)
	}()

	if e.onPromote != nil {
		log.Info("leader_promoted",
			append(e.logWithContext(e.ctx),
				zap.String("token", token),
			)...,
		)
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log := e.getLogger()
					log.Error("onpromote_callback_panic",
						append(e.logWithContext(e.ctx),
							zap.Any("panic", r),
						)...,
					)
				}
			}()
			promoteCtx, cancel := context.WithCancel(e.ctx)
			defer cancel()
			e.onPromote(promoteCtx, token)
		}()
	}
}

func (e *kvElection) attemptPriorityTakeover(payloadBytes []byte) error {
	entry, err := e.kv.Get(e.key)
	if err != nil {
		return err
	}

	var currentPayload leadershipPayload
	if err := json.Unmarshal(entry.Value(), &currentPayload); err != nil {
		return e.attemptAcquire()
	}

	if e.cfg.Priority <= currentPayload.Priority {
		e.leaderID.Store(currentPayload.ID)
		e.revision.Store(entry.Revision())
		return fmt.Errorf("current leader has equal or higher priority: %d >= %d", currentPayload.Priority, e.cfg.Priority)
	}

	newRev, err := e.kv.Update(e.key, payloadBytes, entry.Revision())
	if err != nil {
		// Update failed - revision mismatch means someone else changed it
		// This could mean:
		// 1. Current leader updated (heartbeat) - we'll detect on next check
		// 2. Another instance took over - we'll detect on next check
		// 3. Key was deleted - we'll detect on next check
		return fmt.Errorf("priority takeover failed (revision mismatch): %w", err)
	}

	log := e.getLogger()
	log.Warn("priority_takeover_success",
		append(e.logWithContext(e.ctx),
			zap.String("previous_leader", currentPayload.ID),
			zap.Int("previous_priority", currentPayload.Priority),
			zap.Int("our_priority", e.cfg.Priority),
			zap.Uint64("revision", newRev),
		)...,
	)

	var newPayloadStruct leadershipPayload
	json.Unmarshal(payloadBytes, &newPayloadStruct)

	e.revision.Store(newRev)
	e.token.Store(newPayloadStruct.Token)
	e.becomeLeader(newPayloadStruct.Token, newRev)
	return nil
}

func (e *kvElection) becomeFollower() {
	e.mu.Lock()
	defer e.mu.Unlock()

	fromState := StateInit
	if s := e.state.Load(); s != nil {
		if str, ok := s.(string); ok {
			fromState = str
		}
	}

	wasLeader := e.isLeader.Load()
	e.isLeader.Store(false)
	e.state.Store(StateFollower)
	e.lastTransition.Store(time.Now())

	if wasLeader {
		e.recordLeaderDuration()
		e.leaderStartTime.Store(time.Time{})
	}

	e.recordTransition(fromState, StateFollower)
	e.updateIsLeaderMetric()

	log := e.getLogger()
	log.Info("state_transition",
		append(e.logWithContext(e.ctx),
			zap.String("from_state", fromState),
			zap.String("to_state", StateFollower),
		)...,
	)

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

	currentState := StateInit
	if s := e.state.Load(); s != nil {
		if str, ok := s.(string); ok {
			currentState = str
		}
	}

	if e.cancel != nil {
		e.cancel()
	}

	if wasLeader {
		e.recordLeaderDuration()
		e.leaderStartTime.Store(time.Time{})
	}

	e.isLeader.Store(false)
	e.state.Store(StateStopped)
	e.lastTransition.Store(time.Now())
	e.watcherRunning.Store(false)

	e.recordTransition(currentState, StateStopped)
	e.updateIsLeaderMetric()

	if e.disconnectHandler != nil {
		e.disconnectHandler.stop()
	}

	e.mu.Unlock()

	log := e.getLogger()
	log.Info("election_stopped",
		append(e.logWithContext(e.ctx),
			zap.Bool("was_leader", wasLeader),
		)...,
	)

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
		log.Info("leader_demoted",
			append(e.logWithContext(e.ctx),
				zap.String("reason", "stop"),
			)...,
		)
		e.onDemote()
	}

	return nil
}

func (e *kvElection) StopWithContext(ctx context.Context, opts StopOptions) error {
	e.mu.Lock()

	if e.ctx == nil {
		e.mu.Unlock()
		return ErrAlreadyStopped
	}

	wasLeader := e.isLeader.Load()

	currentState := StateInit
	if s := e.state.Load(); s != nil {
		if str, ok := s.(string); ok {
			currentState = str
		}
	}

	if wasLeader {
		e.recordLeaderDuration()
		e.leaderStartTime.Store(time.Time{})
	}

	if e.cancel != nil {
		e.cancel()
	}

	e.isLeader.Store(false)
	e.state.Store(StateStopped)
	e.lastTransition.Store(time.Now())
	e.watcherRunning.Store(false)

	e.recordTransition(currentState, StateStopped)
	e.updateIsLeaderMetric()

	if e.disconnectHandler != nil {
		e.disconnectHandler.stop()
	}

	e.mu.Unlock()

	if e.connectionMonitor != nil {
		_ = e.connectionMonitor.Stop()
	}

	timeout := opts.Timeout
	if timeout == 0 {
		deadline, ok := ctx.Deadline()
		if ok {
			timeout = time.Until(deadline)
		} else {
			timeout = 5 * time.Second
		}
	}

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		log := e.getLogger()
		log.Warn("shutdown_timeout",
			append(e.logWithContext(ctx),
				zap.Duration("timeout", timeout),
			)...,
		)
		return fmt.Errorf("shutdown timeout exceeded: %v", timeout)
	case <-ctx.Done():
		log := e.getLogger()
		log.Warn("shutdown_cancelled",
			append(e.logWithContext(ctx),
				zap.Error(ctx.Err()),
			)...,
		)
		return ctx.Err()
	}

	e.mu.Lock()
	e.ctx = nil
	e.mu.Unlock()

	log := e.getLogger()
	log.Info("election_stopped",
		append(e.logWithContext(ctx),
			zap.Bool("was_leader", wasLeader),
			zap.Bool("key_deleted", opts.DeleteKey && wasLeader),
		)...,
	)

	if opts.DeleteKey && wasLeader {
		if err := e.kv.Delete(e.key); err != nil {
			log := e.getLogger()
			log.Warn("key_deletion_failed",
				append(e.logWithContext(ctx),
					zap.Error(err),
					zap.String("key", e.key),
				)...,
			)
		} else {
			log := e.getLogger()
			log.Info("key_deleted",
				append(e.logWithContext(ctx),
					zap.String("key", e.key),
				)...,
			)
		}
	}

	if wasLeader && e.onDemote != nil {
		log := e.getLogger()
		log.Info("leader_demoted",
			append(e.logWithContext(ctx),
				zap.String("reason", "stop_with_context"),
				zap.Bool("wait_for_demote", opts.WaitForDemote),
			)...,
		)

		e.mu.RLock()
		onDemote := e.onDemote
		e.mu.RUnlock()

		if onDemote != nil {
			if opts.WaitForDemote {
				// Wait for callback to complete
				done := make(chan struct{})
				go func() {
					onDemote()
					close(done)
				}()

				select {
				case <-done:
				case <-time.After(timeout):
					log.Warn("ondemote_callback_timeout",
						append(e.logWithContext(ctx),
							zap.Duration("timeout", timeout),
						)...,
					)
					return fmt.Errorf("OnDemote callback timeout exceeded: %v", timeout)
				case <-ctx.Done():
					// Context cancelled
					return ctx.Err()
				}
			} else {
				go onDemote()
			}
		}
	}

	return nil
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
		err := &TokenValidationError{
			LocalToken: localToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "no token",
			Err:        nil,
		}
		log := e.getLogger()
		log.Warn("token_validation_failed",
			append(e.logWithContext(ctx),
				zap.Error(err),
				zap.String("error_type", "no_token"),
			)...,
		)
		return false, err
	}

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
		err := &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "token mismatch",
			Err:        nil,
		}
		log := e.getLogger()
		log.Warn("token_validation_failed",
			append(e.logWithContext(ctx),
				zap.Error(err),
				zap.String("error_type", "token_mismatch"),
			)...,
		)
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.IncTokenValidationFailures(e.getMetricsLabels())
		}
		return false, err
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
		err := &TokenValidationError{
			LocalToken: localToken,
			KvToken:    kvToken,
			LeaderID:   e.cfg.InstanceID,
			Reason:     "leader ID mismatch",
			Err:        nil,
		}
		log := e.getLogger()
		log.Warn("token_validation_failed",
			append(e.logWithContext(ctx),
				zap.Error(err),
				zap.String("error_type", "leader_id_mismatch"),
				zap.String("kv_leader_id", leaderID),
			)...,
		)
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.IncTokenValidationFailures(e.getMetricsLabels())
		}
		return false, err
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
