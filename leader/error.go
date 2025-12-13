package leader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Package-level errors that can be returned by the library.
var (
	// ErrNotLeader is returned when an operation requires leadership
	// but the instance is not currently the leader.
	ErrNotLeader = errors.New("not leader")

	// ErrAlreadyStarted is returned when attempting to start an election
	// that is already started.
	ErrAlreadyStarted = errors.New("already started")

	// ErrAlreadyStopped is returned when attempting to stop an election
	// that is already stopped.
	ErrAlreadyStopped = errors.New("already stopped")

	// ErrElectionFailed is returned when leader election fails.
	ErrElectionFailed = errors.New("election failed")

	// ErrHeartbeatFailed is returned when a heartbeat operation fails.
	ErrHeartbeatFailed = errors.New("heartbeat failed")

	// ErrTokenValidationFailed is returned when token validation fails.
	ErrTokenValidationFailed = errors.New("token validation failed")

	// ErrInvalidConfig is returned when the election configuration is invalid.
	ErrInvalidConfig = errors.New("invalid config")

	// ErrBucketNotFound is returned when the specified KV bucket does not exist.
	ErrBucketNotFound = errors.New("bucket not found")

	// ErrPermissionDenied is returned when access to the KV bucket is denied.
	ErrPermissionDenied = errors.New("permission denied")

	// ErrConnectionLost is returned when the NATS connection is lost.
	ErrConnectionLost = errors.New("connection lost")

	// ErrTokenInvalid is returned when a fencing token is invalid.
	ErrTokenInvalid = errors.New("fencing token is invalid")

	// ErrTokenMismatch is returned when a fencing token does not match.
	ErrTokenMismatch = errors.New("fencing token mismatch")
)

// ElectionError represents an error that occurred during an election operation.
// It provides structured error information including error code, instance ID,
// reason, and the underlying error.
//
// Use errors.Is() and errors.As() to check for specific error types:
//
//	var electionErr *ElectionError
//	if errors.As(err, &electionErr) {
//	    log.Printf("Election error [%s]: %s", electionErr.Code, electionErr.Reason)
//	}
type ElectionError struct {
	// Code is a short error code identifying the error type.
	Code string

	// InstanceID is the instance ID where the error occurred.
	InstanceID string

	// Reason is a human-readable description of the error.
	Reason string

	// Err is the underlying error, if any.
	Err error
}

func (e *ElectionError) Unwrap() error {
	return e.Err
}

// NewElectionError creates a new ElectionError with the given parameters.
//
// Example:
//
//	err := NewElectionError(
//	    "ACQUIRE_FAILED",
//	    "instance-1",
//	    "key already exists",
//	    originalErr,
//	)
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

// TokenValidationError represents an error that occurred during token validation.
// It provides detailed information about the token mismatch.
//
// Use errors.Is() and errors.As() to check for this error type:
//
//	var tokenErr *TokenValidationError
//	if errors.As(err, &tokenErr) {
//	    log.Printf("Token validation failed: local=%s, kv=%s",
//	        tokenErr.LocalToken, tokenErr.KvToken)
//	}
type TokenValidationError struct {
	// LocalToken is the token stored locally by this instance.
	LocalToken string

	// KvToken is the token stored in the KV store.
	KvToken string

	// LeaderID is the current leader ID from the KV store.
	LeaderID string

	// Reason is a human-readable description of why validation failed.
	Reason string

	// Err is the underlying error, if any.
	Err error
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

// TimeoutError represents an error that occurred due to a timeout.
// It provides information about which operation timed out and the timeout duration.
//
// Use errors.Is() to check for timeout errors:
//
//	if errors.Is(err, &TimeoutError{}) {
//	    log.Println("Operation timed out")
//	}
//
// Or check for a specific timeout error:
//
//	var timeoutErr *TimeoutError
//	if errors.As(err, &timeoutErr) {
//	    log.Printf("Operation %s timed out after %v",
//	        timeoutErr.Operation, timeoutErr.Timeout)
//	}
type TimeoutError struct {
	// Operation is the name of the operation that timed out.
	Operation string

	// Timeout is the timeout duration that was exceeded.
	Timeout time.Duration

	// Err is the underlying error, if any.
	Err error
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

// NewTimeoutError creates a new TimeoutError with the given parameters.
//
// Example:
//
//	err := NewTimeoutError("heartbeat update", 5*time.Second, nil)
func NewTimeoutError(operation string, timeout time.Duration, err error) *TimeoutError {
	return &TimeoutError{
		Operation: operation,
		Timeout:   timeout,
		Err:       err,
	}
}

// IsPermanentError classifies errors as permanent (demote immediately) vs transient (retry).
// Permanent errors should not be retried.
//
// Permanent errors include:
//   - Revision mismatch (another leader exists)
//   - Key not found
//   - Permission denied
//   - Invalid configuration
//
// Use this function to determine if an error should cause immediate demotion
// or if it should be retried with backoff.
//
// Example:
//
//	if IsPermanentError(err) {
//	    // Demote immediately, don't retry
//	    election.demote()
//	} else {
//	    // Retry with backoff
//	    retryWithBackoff(err)
//	}
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) {
		return false
	}

	if _, ok := err.(*TimeoutError); ok {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	errMsg := strings.ToLower(err.Error())

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
//
// Transient errors include:
//   - Timeouts
//   - Connection lost/refused
//   - Temporary unavailability
//   - Network errors
//
// Use this function to determine if an error should be retried with backoff.
//
// Example:
//
//	if IsTransientError(err) {
//	    // Retry with exponential backoff
//	    retryWithBackoff(err)
//	} else {
//	    // Fail fast
//	    return err
//	}
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	if IsPermanentError(err) {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	if _, ok := err.(*TimeoutError); ok {
		return true
	}

	errMsg := strings.ToLower(err.Error())

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

	return true
}

// ValidationError represents a configuration validation error.
// It provides detailed information about which field failed validation and why.
//
// Use errors.Is() and errors.As() to check for this error type:
//
//	var validationErr *ValidationError
//	if errors.As(err, &validationErr) {
//	    log.Printf("Invalid config: field %s = %v: %s",
//	        validationErr.Field, validationErr.Value, validationErr.Reason)
//	}
type ValidationError struct {
	// Field is the name of the configuration field that failed validation.
	Field string

	// Value is the invalid value that was provided.
	Value interface{}

	// Reason is a human-readable explanation of why validation failed.
	Reason string

	// Err is the underlying error, if any.
	Err error
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

// NewValidationError creates a new ValidationError with the given parameters.
//
// Example:
//
//	err := NewValidationError("TTL", 1*time.Second,
//	    "must be >= HeartbeatInterval * 3")
func NewValidationError(field string, value interface{}, reason string) *ValidationError {
	return &ValidationError{
		Field:  field,
		Value:  value,
		Reason: reason,
	}
}
