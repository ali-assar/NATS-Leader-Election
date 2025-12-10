package leader

import "context"

// HealthChecker defines the interface for health checking.
// This is OPTIONAL - if not provided (nil), health checking is disabled.
//
// If you want health checking, implement this interface. The Check method
// is called before each heartbeat to verify the leader's health.
//
// Examples:
//   - If you have no resources to check, you can return true (always healthy)
//   - If you have a database, check if it's accessible
//   - If you have external dependencies, verify they're reachable
//
// The context can be used for timeout handling and will be cancelled
// if the election is stopped. Implementations should be fast (< 100ms ideally).
type HealthChecker interface {
	// Check returns true if the instance is healthy and should retain leadership.
	// Return false if the instance is unhealthy and should demote.
	//
	// If health check fails MaxConsecutiveFailures times in a row,
	// the leader will automatically demote itself.
	Check(ctx context.Context) bool
}
