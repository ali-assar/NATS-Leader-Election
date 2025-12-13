package leader

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsPermanentError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "permission denied", err: ErrPermissionDenied, want: true},
		{name: "bucket not found", err: ErrBucketNotFound, want: true},
		{name: "revision mismatch", err: errors.New("revision mismatch"), want: true},
		{name: "custom ErrInvalidConfig", err: ErrInvalidConfig, want: true},
		{name: "timeout error", err: errors.New("timeout"), want: false},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: false},
		{name: "nil error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPermanentError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout error", err: context.DeadlineExceeded, want: true},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "connection lost", err: errors.New("connection lost"), want: true},
		{name: "permission denied", err: ErrPermissionDenied, want: false},
		{name: "nil error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTransientError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		min     time.Duration
		max     time.Duration
	}{
		{name: "attempt 0", attempt: 0, min: 40 * time.Millisecond, max: 60 * time.Millisecond},
		{name: "attempt 1", attempt: 1, min: 90 * time.Millisecond, max: 110 * time.Millisecond},
		{name: "attempt 2", attempt: 2, min: 180 * time.Millisecond, max: 220 * time.Millisecond},
		{name: "attempt 10 (capped)", attempt: 10, min: 4500 * time.Millisecond, max: 5500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateBackoff(DefaultBackoffConfig(), tt.attempt)
			assert.GreaterOrEqual(t, got, tt.min)
			assert.LessOrEqual(t, got, tt.max)
		})
	}
}

func TestCalculateBackoff_MaxLimit(t *testing.T) {
	cfg := BackoffConfig{
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            0.1,
	}

	backoff := CalculateBackoff(cfg, 100)
	assert.LessOrEqual(t, backoff, 1100*time.Millisecond)
}

func TestRetryWithBackoff_Success(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:   5,
		BackoffConfig: DefaultBackoffConfig(),
	}

	attempt := 0
	fn := func() error {
		attempt++
		if attempt == 1 {
			return nil // Succeed on first attempt
		}
		return errors.New("should not be called")
	}

	err := RetryWithBackoff(context.Background(), cfg, fn)
	assert.NoError(t, err)
	assert.Equal(t, 1, attempt, "should only call once")
}

func TestRetryWithBackoff_TransientError(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:   5,
		BackoffConfig: DefaultBackoffConfig(),
	}

	attempt := 0
	fn := func() error {
		attempt++
		if attempt < 3 {
			return errors.New("temporary failure")
		}
		return nil // Succeed on third attempt
	}

	err := RetryWithBackoff(context.Background(), cfg, fn)
	assert.NoError(t, err)
	assert.Equal(t, 3, attempt, "should retry until success")
}

func TestRetryWithBackoff_PermanentError(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:   5,
		BackoffConfig: DefaultBackoffConfig(),
	}

	attempt := 0
	fn := func() error {
		attempt++
		return ErrPermissionDenied // Permanent error
	}

	err := RetryWithBackoff(context.Background(), cfg, fn)
	assert.Error(t, err)
	assert.Equal(t, 1, attempt, "should not retry permanent errors")
	assert.ErrorIs(t, err, ErrPermissionDenied)
}

func TestRetryWithBackoff_MaxAttempts(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:   3,
		BackoffConfig: DefaultBackoffConfig(),
	}

	attempt := 0
	fn := func() error {
		attempt++
		return errors.New("always fails")
	}

	err := RetryWithBackoff(context.Background(), cfg, fn)
	assert.Error(t, err)
	// MaxAttempts=3 means: attempt 0, 1, 2 (3 attempts total)
	assert.Equal(t, 3, attempt, "should retry exactly MaxAttempts times")
	assert.Contains(t, err.Error(), "max attempts")
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:   10,
		BackoffConfig: DefaultBackoffConfig(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	attempt := 0
	fn := func() error {
		attempt++
		return errors.New("always fails")
	}

	err := RetryWithBackoff(ctx, cfg, fn)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Greater(t, attempt, 0, "should have attempted at least once")
}

func TestCircuitBreaker_Open(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	for i := 0; i < 3; i++ {
		err := cb.Call(func() error {
			return errors.New("failure")
		})
		assert.Error(t, err)
	}
	err := cb.Call(func() error {
		return nil
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
}

func TestCircuitBreaker_Cooldown(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)
	for i := 0; i < 3; i++ {
		_ = cb.Call(func() error { return errors.New("failure") })
	}
	time.Sleep(150 * time.Millisecond)

	err := cb.Call(func() error {
		return nil
	})
	assert.NoError(t, err)
}
