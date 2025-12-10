package leader

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// Logger defines the interface for structured logging.
// Implementations should use zap.Field for structured data.
type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	Fatal(msg string, fields ...zap.Field)
}

func (e *kvElection) getLogger() Logger {
	if e.cfg.Logger != nil {
		return e.cfg.Logger
	}
	return &noOpLogger{}
}

func (e *kvElection) logWithContext(ctx context.Context) []zap.Field {
	fields := []zap.Field{
		zap.String("instance_id", e.cfg.InstanceID),
		zap.String("group", e.cfg.Group),
		zap.String("bucket", e.cfg.Bucket),
	}

	// Add correlation ID if present in context
	if correlationID := ctx.Value("correlation_id"); correlationID != nil {
		fields = append(fields, zap.String("correlation_id", correlationID.(string)))
	}

	return fields
}

type noOpLogger struct{}

func (n *noOpLogger) Debug(msg string, fields ...zap.Field) {}
func (n *noOpLogger) Info(msg string, fields ...zap.Field)  {}
func (n *noOpLogger) Warn(msg string, fields ...zap.Field)  {}
func (n *noOpLogger) Error(msg string, fields ...zap.Field) {}
func (n *noOpLogger) Fatal(msg string, fields ...zap.Field) {}

// classifyErrorType returns a string classification of the error type for logging
func classifyErrorType(err error) string {
	if err == nil {
		return "none"
	}

	if IsPermanentError(err) {
		return "permanent"
	}
	if IsTransientError(err) {
		return "transient"
	}

	// Check for specific error types
	if errors.Is(err, ErrNotLeader) {
		return "not_leader"
	}
	if errors.Is(err, ErrElectionFailed) {
		return "election_failed"
	}
	if errors.Is(err, ErrTokenInvalid) {
		return "token_invalid"
	}
	if errors.Is(err, ErrAlreadyStarted) {
		return "already_started"
	}
	if errors.Is(err, ErrAlreadyStopped) {
		return "already_stopped"
	}

	return "unknown"
}
