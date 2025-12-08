package leader

import (
	"context"
	"time"
)

const (
	defaultValidationInterval = 5 * time.Second
	defaultValidationTimeout  = 2 * time.Second
)

// validationLoop periodically validates the fencing token.
// If validation fails, the leader is demoted.
func (e *kvElection) validationLoop(ctx context.Context) {
	interval := defaultValidationInterval
	if e.cfg.ValidationInterval > 0 {
		interval = e.cfg.ValidationInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveFailures := 0
	maxFailures := 2

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.IsLeader() {
				return
			}

			validationCtx, cancel := context.WithTimeout(ctx, defaultValidationTimeout)
			isValid, err := e.validateToken(validationCtx)
			cancel()

			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					e.handleValidationFailure(err)
					return
				}
				continue
			}

			if !isValid {
				e.handleValidationFailure(ErrTokenInvalid)
				return
			}

			consecutiveFailures = 0
		}
	}
}

func (e *kvElection) handleValidationFailure(err error) {
	e.becomeFollower()

	e.mu.RLock()
	onDemote := e.onDemote
	e.mu.RUnlock()

	if onDemote != nil {
		onDemote()
	}
}
