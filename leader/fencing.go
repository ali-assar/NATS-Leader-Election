package leader

import (
	"context"
	"time"

	"go.uber.org/zap"
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
				log := e.getLogger()
				log.Warn("token_validation_failed",
					append(e.logWithContext(ctx),
						zap.Error(err),
						zap.String("error_type", classifyErrorType(err)),
						zap.Int("consecutive_failures", consecutiveFailures),
					)...,
				)
				if consecutiveFailures >= maxFailures {
					e.handleValidationFailure(err)
					return
				}
				continue
			}

			if !isValid {
				log := e.getLogger()
				log.Warn("token_validation_failed",
					append(e.logWithContext(ctx),
						zap.Error(ErrTokenInvalid),
						zap.String("error_type", "token_invalid"),
					)...,
				)
				e.handleValidationFailure(ErrTokenInvalid)
				return
			}

			if consecutiveFailures > 0 {
				consecutiveFailures = 0
				log := e.getLogger()
				log.Debug("token_validation_recovered",
					append(e.logWithContext(ctx))...,
				)
			}
		}
	}
}

func (e *kvElection) handleValidationFailure(err error) {
	log := e.getLogger()
	log.Error("demoting_due_to_validation_failure",
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
				zap.String("reason", "token_validation_failure"),
			)...,
		)
		onDemote()
	}
}
