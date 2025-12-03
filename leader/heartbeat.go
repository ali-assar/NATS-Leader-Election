package leader

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// heartbeatLoop runs in a goroutine and periodically updates the leadership key
// to keep the TTL alive. It stops when the context is cancelled or if we're no longer leader.
func (e *kvElection) heartbeatLoop(ctx context.Context) {
	// Create a ticker that fires every HeartbeatInterval
	ticker := time.NewTicker(e.cfg.HeartbeatInterval)
	defer ticker.Stop() // Always stop the ticker when function exits

	consecutiveFailures := 0
	maxFailures := 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.IsLeader() {
				// We're not leader anymore, reset failures and exit
				consecutiveFailures = 0
				return
			}

			// TODO: Step 4.2 - Implement the actual heartbeat update logic here
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

			// Since KeyValue interface doesn't support context, we use a goroutine + channel pattern
			updateTimeout := e.cfg.HeartbeatInterval / 2 // Timeout is half of heartbeat interval
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
// Permanent errors indicate we've lost leadership or access.
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Permanent errors - demote immediately
	permanentPatterns := []string{
		"revision mismatch", // Someone else updated the key
		"key not found",     // Key was deleted
		"permission denied", // Access revoked
		"bucket not found",  // Bucket was deleted
	}

	for _, pattern := range permanentPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	// Transient errors - retry
	// Examples: timeout, connection lost, temporary failure, deadline exceeded
	transientPatterns := []string{
		"timeout",
		"deadline exceeded",
		"connection lost",
		"temporary",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return false // Transient, not permanent
		}
	}

	// Default: treat unknown errors as transient (safer to retry)
	return false
}

// handleHeartbeatFailure handles permanent heartbeat failures by demoting the leader.
func (e *kvElection) handleHeartbeatFailure(err error) {
	// Demote to follower
	e.becomeFollower()

	// Call onDemote callback if set
	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		onDemote()
	}
}
