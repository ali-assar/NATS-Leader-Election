package leader

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// heartbeatLoop periodically updates the leadership key to keep the TTL alive.
func (e *kvElection) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.HeartbeatInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	maxFailures := 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.IsLeader() {
				return
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
				rev, updateErr := e.kv.Update(e.key, payloadBytes, currentRev)
				resultChan <- updateResult{rev: rev, err: updateErr}
			}()

			var newRev uint64
			var updateErr error
			select {
			case <-ctx.Done():
				return
			case <-time.After(updateTimeout):
				updateErr = &timeoutError{message: "heartbeat update timeout"}
			case result := <-resultChan:
				newRev = result.rev
				updateErr = result.err
			}

			if updateErr != nil {
				if isPermanentError(updateErr) {
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

type timeoutError struct {
	message string
}

func (e *timeoutError) Error() string {
	return e.message
}

// isPermanentError classifies errors as permanent (demote immediately) vs transient (retry).
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	permanentPatterns := []string{
		"revision mismatch",
		"key not found",
		"permission denied",
		"bucket not found",
	}

	for _, pattern := range permanentPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	transientPatterns := []string{
		"timeout",
		"deadline exceeded",
		"connection lost",
		"temporary",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return false
		}
	}

	return false
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
