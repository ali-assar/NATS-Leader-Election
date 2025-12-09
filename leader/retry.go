package leader

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

type BackoffConfig struct {
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	Jitter            float64
}

func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        5 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            0.1,
	}
}

func CalculateBackoff(cfg BackoffConfig, attempt int) time.Duration {
	backoff := float64(cfg.InitialBackoff) * math.Pow(cfg.BackoffMultiplier, float64(attempt))
	if backoff > float64(cfg.MaxBackoff) {
		backoff = float64(cfg.MaxBackoff)
	}

	jitterAmount := backoff * cfg.Jitter * (rand.Float64()*2 - 1)
	finalBackoff := backoff + jitterAmount
	if finalBackoff < 0 {
		finalBackoff = backoff
	}
	return time.Duration(finalBackoff)
}

type RetryConfig struct {
	MaxAttempts    int
	BackoffConfig  BackoffConfig
	CircuitBreaker *CircuitBreaker
}

func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() error) error {
	attempt := 0

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the function
		var err error
		if cfg.CircuitBreaker != nil {
			cbErr := cfg.CircuitBreaker.Call(fn)
			// Check if circuit breaker is open (don't retry)
			if cbErr != nil && cbErr.Error() == "circuit breaker is open" {
				return cbErr
			}
			err = cbErr
		} else {
			err = fn()
		}

		if err == nil {
			return nil
		}

		if IsPermanentError(err) {
			return err
		}

		// Check max attempts after the call (attempt is 0-indexed)
		// If MaxAttempts=3, we want attempts 0, 1, 2 (3 total)
		// So we check if attempt >= MaxAttempts-1 after incrementing
		if cfg.MaxAttempts > 0 && attempt >= cfg.MaxAttempts-1 {
			return fmt.Errorf("max attempts (%d) exceeded: %w", cfg.MaxAttempts, err)
		}

		// Calculate backoff for current attempt
		backoff := CalculateBackoff(cfg.BackoffConfig, attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			attempt++
		}
	}
}

type CircuitState int

const (
	CircuitStateClosed CircuitState = iota
	CircuitStateOpen
	CircuitStateHalfOpen
)

type CircuitBreaker struct {
	failureThreshold int
	cooldownPeriod   time.Duration
	state            CircuitState
	failures         int
	lastFailureTime  time.Time
	mu               sync.Mutex
}

func NewCircuitBreaker(failureThreshold int, cooldownPeriod time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		cooldownPeriod:   cooldownPeriod,
		state:            CircuitStateClosed,
	}
}

func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitStateOpen {
		if time.Since(cb.lastFailureTime) < cb.cooldownPeriod {
			return fmt.Errorf("circuit breaker is open")
		}
		cb.state = CircuitStateHalfOpen
	}

	err := fn()
	if err != nil {
		cb.failures++
		cb.lastFailureTime = time.Now()
		if cb.failures >= cb.failureThreshold {
			cb.state = CircuitStateOpen
		}
		return err
	}

	cb.failures = 0
	cb.state = CircuitStateClosed
	return nil
}
