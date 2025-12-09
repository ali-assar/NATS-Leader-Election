package leader

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
	"github.com/stretchr/testify/assert"
)

func TestValidateToken_Valid(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 1 * time.Second,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Failed to create election")

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)

	assert.True(t, election.IsLeader(), "Instance should be leader")
	defer election.Stop()

	valid, err := election.ValidateToken(context.Background())
	assert.NoError(t, err, "Failed to validate token")
	assert.True(t, valid, "Token should be valid")
}

func TestValidateToken_Invalid_Mismatch(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 1 * time.Second,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Failed to create election")

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Instance should be leader")

	originalToken := election.Token()
	assert.NotEmpty(t, originalToken, "Should have a token")

	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Get current entry to get the revision
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	// Update with different leader ID and token (simulating another instance taking over)
	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": "different-token-123",
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	// Update the key with new leader info
	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err, "Failed to update key with new leader")

	// Now validate - should fail because token doesn't match
	valid, err := election.ValidateToken(context.Background())
	assert.False(t, valid, "Token should be invalid (mismatch)")

	assert.Error(t, err, "Should return error for invalid token")
	var tokenErr *TokenValidationError
	assert.ErrorAs(t, err, &tokenErr, "Error should be TokenValidationError")
	assert.Contains(t, tokenErr.Reason, "token mismatch", "Error reason should indicate token mismatch")

	// Verify the local token is still the old one
	assert.Equal(t, originalToken, election.Token(), "Local token should still be the old one")

	defer election.Stop()
}

func TestValidateToken_Invalid_DifferentLeader(t *testing.T) {
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 1 * time.Second,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Failed to create election")

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Instance should be leader")

	originalToken := election.Token()
	assert.NotEmpty(t, originalToken, "Should have a token")
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Get current entry to get the revision
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": "different-token-123",
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err, "Failed to update key with new leader")

	valid, err := election.ValidateToken(context.Background())
	assert.False(t, valid, "Token should be invalid (different leader)")
	assert.Error(t, err, "Should return error for invalid token")
	var tokenErr *TokenValidationError
	assert.ErrorAs(t, err, &tokenErr, "Error should be TokenValidationError")
	// Token is checked first, so even though leader ID is different,
	// the error will be "token mismatch" (not "leader ID mismatch")
	assert.Contains(t, tokenErr.Reason, "token mismatch", "Error reason should indicate token mismatch (checked before leader ID)")

	assert.Equal(t, originalToken, election.Token(), "Local token should still be the old one")

	defer election.Stop()
}

func TestValidateToken_Invalid_LeaderIDMismatch(t *testing.T) {
	// This test specifically checks leader ID validation
	// by keeping the token the same but changing the leader ID
	// (This is an edge case that shouldn't happen in practice,
	// but tests the leader ID check logic)
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 1 * time.Second,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Failed to create election")

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Instance should be leader")

	originalToken := election.Token()
	assert.NotEmpty(t, originalToken, "Should have a token")

	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Get current entry to get the revision
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	// Update with SAME token but DIFFERENT leader ID
	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": originalToken, // Same token (unrealistic but tests leader ID check)
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err, "Failed to update key with different leader ID")

	valid, err := election.ValidateToken(context.Background())
	assert.False(t, valid, "Token should be invalid (leader ID mismatch)")
	assert.Error(t, err, "Should return error for invalid token")
	var tokenErr *TokenValidationError
	assert.ErrorAs(t, err, &tokenErr, "Error should be TokenValidationError")
	// Now it should be "leader ID mismatch" because token matches
	assert.Contains(t, tokenErr.Reason, "leader ID mismatch", "Error reason should indicate leader ID mismatch")

	defer election.Stop()
}

func TestValidateToken_Invalid_NoTokenInKV(t *testing.T) {
	// Test scenario: Leader has local token, but key is deleted from KV
	// (simulating key expiration or manual deletion)
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 1 * time.Second,
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err, "Failed to create election")

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Instance should be leader")

	originalToken := election.Token()
	assert.NotEmpty(t, originalToken, "Leader should have a token")

	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Delete the key from KV
	err = mockKV.Delete("test-group")
	assert.NoError(t, err, "Should be able to delete the key")

	// Now validate - should fail because key doesn't exist in KV
	valid, err := election.ValidateToken(context.Background())
	assert.False(t, valid, "Token should be invalid (key not found)")
	assert.Error(t, err, "Should return error for invalid token")
	var tokenErr *TokenValidationError
	assert.ErrorAs(t, err, &tokenErr, "Error should be TokenValidationError")

	assert.Contains(t, tokenErr.Reason, "failed to get token from KV", "Error reason should indicate failed to get token from KV")
	assert.Contains(t, tokenErr.Err.Error(), "key not found", "Underlying error should be 'key not found'")

	// Verify local token is still there (hasn't been cleared)
	assert.Equal(t, originalToken, election.Token(), "Local token should still exist")

	defer election.Stop()
}

func TestValidationLoop_ValidToken(t *testing.T) {
	// Test that validation loop continues running when token remains valid
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond, // Reduced for faster testing
		ValidationInterval: 200 * time.Millisecond, // Fast validation for testing (>= HeartbeatInterval)
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Wait for at least one validation cycle to complete
	// Validation interval is 200ms, so wait 300ms to ensure it runs
	time.Sleep(300 * time.Millisecond)

	// Should still be leader (token is valid)
	assert.True(t, election.IsLeader(), "Should still be leader after validation")

	// Wait for another validation cycle
	time.Sleep(300 * time.Millisecond)

	// Should still be leader
	assert.True(t, election.IsLeader(), "Should still be leader after multiple validations")

	defer election.Stop()
}

func TestValidationLoop_InvalidToken(t *testing.T) {
	// Test that validation loop demotes leader when token becomes invalid
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond, // Reduced for faster testing
		ValidationInterval: 200 * time.Millisecond, // Fast validation for testing (>= HeartbeatInterval)
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	var demoteCalled bool
	election.OnDemote(func() {
		demoteCalled = true
	})

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Get mock KV and update with different token (simulating another leader)
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	// Update with different leader/token
	newPayload := map[string]interface{}{
		"id":    "instance-2",
		"token": "different-token-456",
	}
	newPayloadBytes, err := json.Marshal(newPayload)
	assert.NoError(t, err)

	_, err = mockKV.Update("test-group", newPayloadBytes, entry.Revision())
	assert.NoError(t, err)

	// Wait for validation loop to run and detect invalid token
	// Validation interval is 200ms, wait a bit longer to ensure it runs
	waitForLeader(t, election, false, 500*time.Millisecond)

	// Should be demoted
	assert.False(t, election.IsLeader(), "Should be demoted after token becomes invalid")

	// Wait for onDemote callback
	waitForCondition(t, func() bool {
		return demoteCalled
	}, 500*time.Millisecond, "OnDemote callback")

	assert.True(t, demoteCalled, "OnDemote callback should be called")

	defer election.Stop()
}

func TestValidationLoop_Timeout(t *testing.T) {
	// Test that validation loop handles timeout gracefully
	cfg := ElectionConfig{
		Bucket:             "leaders",
		Group:              "test-group",
		InstanceID:         "instance-1",
		TTL:                10 * time.Second,
		HeartbeatInterval:  200 * time.Millisecond, // Reduced for faster testing
		ValidationInterval: 200 * time.Millisecond, // Fast validation for testing (>= HeartbeatInterval)
	}

	nc := natsmock.NewMockConn()
	election, err := NewElection(newMockConnAdapter(nc), cfg)
	assert.NoError(t, err)

	err = election.Start(context.Background())
	assert.NoError(t, err)

	waitForLeader(t, election, true, 1*time.Second)
	assert.True(t, election.IsLeader(), "Should be leader")

	// Get mock KV and make Get() operation slow (simulating timeout)
	js, err := nc.JetStream()
	assert.NoError(t, err)
	mockKV, err := js.KeyValue("leaders")
	assert.NoError(t, err)

	// Store the current entry before making Get() slow
	entry, err := mockKV.Get("test-group")
	assert.NoError(t, err)
	assert.NotNil(t, entry)

	// Store entry data for later use
	entryValue := entry.Value()
	entryRev := entry.Revision()

	// Make Get() slow (longer than validation timeout of 2 seconds)
	// This simulates a network delay or slow KV store
	slowDelay := 3 * time.Second
	mockKV.GetFunc = func(key string) (natsmock.Entry, error) {
		time.Sleep(slowDelay)
		// Return the stored entry after delay
		return &natsmock.MockEntryImpl{
			KeyVal:   key,
			ValueVal: entryValue,
			RevVal:   entryRev,
		}, nil
	}

	// Wait for validation loop to run and timeout
	// The validation timeout is 2 seconds (defaultValidationTimeout)
	// maxFailures = 2, so it will timeout twice before demoting
	// First validation: starts after 200ms, times out after 2s
	// Second validation: starts after another 200ms, times out after 2s
	// After 2 consecutive failures, it should demote

	// Wait for demotion (should happen after 2 consecutive timeouts)
	waitForLeader(t, election, false, 6*time.Second)

	// Should be demoted after timeout failures
	assert.False(t, election.IsLeader(), "Should be demoted after validation timeouts")

	defer election.Stop()
}
