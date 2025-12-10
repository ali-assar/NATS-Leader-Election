package leader

import (
	"context"
	"encoding/json"
	"time"
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
					e.healthFailureCount.Add(1)
					if e.healthFailureCount.Load() >= int32(maxHealthFailures) {
						e.handleHealthCheckFailure()
						return
					}
					continue
				}
				e.healthFailureCount.Store(0)
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
				if IsPermanentError(updateErr) {
					e.handleHeartbeatFailure(updateErr)
					return
				}

				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					e.handleHeartbeatFailure(updateErr)
					return
				}
				continue
			}

			e.revision.Store(newRev)
			consecutiveFailures = 0
			e.lastHeartbeat.Store(time.Now())
		}
	}
}

func (e *kvElection) handleHeartbeatFailure(err error) {
	e.becomeFollower()

	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		onDemote()
	}
}

func (e *kvElection) handleHealthCheckFailure() {
	e.becomeFollower()

	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		onDemote()
	}
}
