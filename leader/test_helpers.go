package leader

import (
	"testing"
	"time"
)

// WaitForCondition waits for a condition to become true within the given timeout.
// This is a test helper function that polls the condition at 10ms intervals.
//
// Example:
//
//	WaitForCondition(t, func() bool {
//	    return election.IsLeader()
//	}, 5*time.Second, "election to become leader")
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond) // Small polling interval
	}
	t.Fatalf("Timeout waiting for condition: %s", msg)
}

// WaitForLeader waits for an election to reach the expected leader state.
// This is a convenience wrapper around WaitForCondition.
//
// Example:
//
//	WaitForLeader(t, election, true, 5*time.Second)  // Wait to become leader
//	WaitForLeader(t, election, false, 5*time.Second) // Wait to become follower
func WaitForLeader(t *testing.T, election Election, expectLeader bool, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, func() bool {
		return election.IsLeader() == expectLeader
	}, timeout, "leader state")
}

// WaitForHeartbeat waits for a heartbeat to occur after the initial heartbeat time.
// This verifies that the heartbeat loop is running and updating the leadership key.
//
// Example:
//
//	initialHeartbeat := election.Status().LastHeartbeat
//	WaitForHeartbeat(t, election, initialHeartbeat, 2*time.Second)
func WaitForHeartbeat(t *testing.T, election Election, initialHeartbeat time.Time, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, func() bool {
		status := election.Status()
		return status.LastHeartbeat.After(initialHeartbeat)
	}, timeout, "heartbeat")
}
