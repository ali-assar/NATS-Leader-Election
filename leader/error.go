package leader

import (
	"errors"
	"fmt"
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
