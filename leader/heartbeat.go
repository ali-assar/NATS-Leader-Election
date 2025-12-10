package leader

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"
)

// heartbeatLoop periodically updates the leadership key to keep the TTL alive.
func (e *kvElection) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.HeartbeatInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	maxFailures := 3

	maxHealthFailures := e.cfg.MaxConsecutiveFailures
	if maxHealthFailures <= 0 {
		maxHealthFailures = 3
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.IsLeader() {
				return
			}

			if e.cfg.HealthChecker != nil {
				healthCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				healthy := e.cfg.HealthChecker.Check(healthCtx)
				cancel()
				if !healthy {
					failureCount := e.healthFailureCount.Add(1)
					log := e.getLogger()
					log.Warn("health_check_failed",
						append(e.logWithContext(ctx),
							zap.Int32("failure_count", failureCount),
							zap.Int("threshold", maxHealthFailures),
						)...,
					)
					if failureCount >= int32(maxHealthFailures) {
						e.handleHealthCheckFailure()
						return
					}
					continue
				}
				// Reset on success
				if e.healthFailureCount.Load() > 0 {
					e.healthFailureCount.Store(0)
					log := e.getLogger()
					log.Debug("health_check_recovered",
						append(e.logWithContext(ctx))...,
					)
				}
			}

			currentRev := e.revision.Load()

			token := e.Token()
			payload := map[string]interface{}{
				"id":    e.cfg.InstanceID,
				"token": token,
			}
			payloadBytes, err := json.Marshal(payload)
			if err != nil {
				consecutiveFailures++
				log := e.getLogger()
				log.Error("heartbeat_failed",
					append(e.logWithContext(ctx),
						zap.Error(err),
						zap.String("error_type", "marshal_error"),
						zap.Int("consecutive_failures", consecutiveFailures),
					)...,
				)
				if consecutiveFailures >= maxFailures {
					e.handleHeartbeatFailure(err)
					return
				}
				continue
			}

			updateTimeout := e.cfg.HeartbeatInterval / 2
			if updateTimeout < 1*time.Second {
				updateTimeout = 1 * time.Second
			}

			type updateResult struct {
				rev uint64
				err error
			}
			resultChan := make(chan updateResult, 1)

			go func() {
				// Pass TTL option to Update to refresh the key's expiration
				var opts []interface{}
				if e.cfg.TTL > 0 {
					opts = append(opts, e.cfg.TTL)
				}
				rev, updateErr := e.kv.Update(e.key, payloadBytes, currentRev, opts...)
				resultChan <- updateResult{rev: rev, err: updateErr}
			}()

			var newRev uint64
			var updateErr error
			select {
			case <-ctx.Done():
				return
			case <-time.After(updateTimeout):
				updateErr = NewTimeoutError("heartbeat update", updateTimeout, nil)
			case result := <-resultChan:
				newRev = result.rev
				updateErr = result.err
			}

			if updateErr != nil {
				log := e.getLogger()
				errorType := classifyErrorType(updateErr)
				if IsPermanentError(updateErr) {
					log.Error("heartbeat_failed",
						append(e.logWithContext(ctx),
							zap.Error(updateErr),
							zap.String("error_type", errorType),
							zap.Uint64("revision", currentRev),
							zap.Bool("permanent", true),
						)...,
					)
					e.handleHeartbeatFailure(updateErr)
					return
				}

				consecutiveFailures++
				log.Warn("heartbeat_failed",
					append(e.logWithContext(ctx),
						zap.Error(updateErr),
						zap.String("error_type", errorType),
						zap.Uint64("revision", currentRev),
						zap.Int("consecutive_failures", consecutiveFailures),
					)...,
				)
				if consecutiveFailures >= maxFailures {
					e.handleHeartbeatFailure(updateErr)
					return
				}
				continue
			}

			e.revision.Store(newRev)
			if consecutiveFailures > 0 {
				consecutiveFailures = 0
				log := e.getLogger()
				log.Debug("heartbeat_recovered",
					append(e.logWithContext(ctx),
						zap.Uint64("revision", newRev),
					)...,
				)
			}
			e.lastHeartbeat.Store(time.Now())
		}
	}
}

func (e *kvElection) handleHeartbeatFailure(err error) {
	log := e.getLogger()
	log.Error("demoting_due_to_heartbeat_failure",
		append(e.logWithContext(e.ctx),
			zap.Error(err),
			zap.String("error_type", classifyErrorType(err)),
		)...,
	)

	e.becomeFollower()

	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		log.Info("leader_demoted",
			append(e.logWithContext(e.ctx),
				zap.String("reason", "heartbeat_failure"),
			)...,
		)
		onDemote()
	}
}

func (e *kvElection) handleHealthCheckFailure() {
	log := e.getLogger()
	failureCount := e.healthFailureCount.Load()
	log.Error("demoting_due_to_health_check_failure",
		append(e.logWithContext(e.ctx),
			zap.Int32("failure_count", failureCount),
		)...,
	)

	e.becomeFollower()

	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		log.Info("leader_demoted",
			append(e.logWithContext(e.ctx),
				zap.String("reason", "health_check_failure"),
			)...,
		)
		onDemote()
	}
}
