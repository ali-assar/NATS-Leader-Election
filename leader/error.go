package leader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotLeader      = errors.New("not leader")
	ErrAlreadyStarted = errors.New("already started")
	ErrAlreadyStopped = errors.New("already stopped")

	ErrElectionFailed        = errors.New("election failed")
	ErrHeartbeatFailed       = errors.New("heartbeat failed")
	ErrTokenValidationFailed = errors.New("token validation failed")

	ErrInvalidConfig    = errors.New("invalid config")
	ErrBucketNotFound   = errors.New("bucket not found")
	ErrPermissionDenied = errors.New("permission denied")

	ErrConnectionLost = errors.New("connection lost")

	ErrTokenInvalid  = errors.New("fencing token is invalid")
	ErrTokenMismatch = errors.New("fencing token mismatch")
)

type ElectionError struct {
	Code       string
	InstanceID string
	Reason     string
	Err        error
}

func (e *ElectionError) Unwrap() error {
	return e.Err
}

func NewElectionError(code string, instanceID string, reason string, err error) *ElectionError {
	return &ElectionError{
		Code:       code,
		InstanceID: instanceID,
		Reason:     reason,
		Err:        err,
	}
}

func (e *ElectionError) Error() string {
	msg := fmt.Sprintf("election error [%s]", e.Code)

	if e.InstanceID != "" {
		msg += fmt.Sprintf(" for instance %s", e.InstanceID)
	}

	if e.Reason != "" {
		msg += fmt.Sprintf(": %s", e.Reason)
	}

	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}

	return msg
}

type TokenValidationError struct {
	LocalToken string
	KvToken    string
	LeaderID   string
	Reason     string
	Err        error
}

func (e *TokenValidationError) Error() string {
	msg := fmt.Sprintf("token validation failed: %s", e.Reason)
	if e.LocalToken != "" && e.KvToken != "" {
		msg += fmt.Sprintf(" (local: %s, kv: %s)", e.LocalToken, e.KvToken)
	}
	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}
	return msg
}

func (e *TokenValidationError) Unwrap() error {
	return e.Err
}

type TimeoutError struct {
	Operation string
	Timeout   time.Duration
	Err       error
}

func (e *TimeoutError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("operation %s timed out after %v: %v",
			e.Operation, e.Timeout, e.Err)
	}
	if e.Operation != "" {
		return fmt.Sprintf("operation %s timed out after %v",
			e.Operation, e.Timeout)
	}
	return fmt.Sprintf("operation timed out after %v", e.Timeout)
}

func (e *TimeoutError) Unwrap() error {
	return e.Err
}

func (e *TimeoutError) Is(target error) bool {
	if t, ok := target.(*TimeoutError); ok {
		if e.Operation != "" && t.Operation != "" {
			return e.Operation == t.Operation && e.Timeout == t.Timeout
		}
		return e.Timeout == t.Timeout
	}
	return false
}

func NewTimeoutError(operation string, timeout time.Duration, err error) *TimeoutError {
	return &TimeoutError{
		Operation: operation,
		Timeout:   timeout,
		Err:       err,
	}
}

// IsPermanentError classifies errors as permanent (demote immediately) vs transient (retry).
// Permanent errors should not be retried.
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) {
		return false
	}

	// Check for timeout errors (transient, not permanent)
	if _, ok := err.(*TimeoutError); ok {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Check for permanent error patterns
	permanentPatterns := []string{
		"revision mismatch",
		"key not found",
		"permission denied",
		"bucket not found",
		"access denied",
		"invalid",
		"authentication",
	}

	for _, pattern := range permanentPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	if errors.Is(err, ErrInvalidConfig) {
		return true
	}
	if errors.Is(err, ErrPermissionDenied) {
		return true
	}
	if errors.Is(err, ErrBucketNotFound) {
		return true
	}

	return false
}

// IsTransientError classifies errors as transient (retry with backoff) vs permanent (fail fast).
// Transient errors are typically network issues, timeouts, or temporary unavailability.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Permanent errors are not transient
	if IsPermanentError(err) {
		return false
	}

	// Check for context timeout (transient)
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check for TimeoutError (transient)
	if _, ok := err.(*TimeoutError); ok {
		return true
	}

	errMsg := strings.ToLower(err.Error())

	// Check for transient error patterns
	transientPatterns := []string{
		"timeout",
		"deadline exceeded",
		"connection lost",
		"connection refused",
		"temporary",
		"unavailable",
		"network",
		"i/o timeout",
		"connection reset",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	// Default: if not permanent, assume transient (conservative approach)
	return true
}

type ValidationError struct {
	Field  string
	Value  interface{}
	Reason string
	Err    error
}

func (e *ValidationError) Error() string {
	msg := fmt.Sprintf("invalid configuration: field %s", e.Field)
	if e.Value != nil {
		msg += fmt.Sprintf(" = %v", e.Value)
	}
	if e.Reason != "" {
		msg += fmt.Sprintf(": %s", e.Reason)
	}
	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}
	return msg
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

func NewValidationError(field string, value interface{}, reason string) *ValidationError {
	return &ValidationError{
		Field:  field,
		Value:  value,
		Reason: reason,
	}
}
