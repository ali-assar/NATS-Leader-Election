package leader

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/internal/natsmock"
)

// BenchmarkAcquireLeadership benchmarks the leader acquisition operation.
// This measures the time to create a leadership key in the KV store.
func BenchmarkAcquireLeadership(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter := NewMockConnAdapter(nc)
		election, err := NewElection(adapter, cfg)
		if err != nil {
			b.Fatal(err)
		}

		ctx := context.Background()
		if err := election.Start(ctx); err != nil {
			b.Fatal(err)
		}

		// Wait for acquisition
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			if election.IsLeader() {
				break
			}
			time.Sleep(1 * time.Millisecond)
		}

		_ = election.Stop()
	}
}

// BenchmarkHeartbeatUpdate benchmarks the heartbeat update operation.
// This measures the time to update the leadership key with a new revision.
func BenchmarkHeartbeatUpdate(b *testing.B) {
	nc := natsmock.NewMockConn()

	// Get the KV store to directly benchmark Update
	js, _ := nc.JetStream()
	kv, _ := js.KeyValue("leaders")

	// Pre-create the key
	payload := []byte(`{"id":"instance-1","token":"test-token"}`)
	rev, err := kv.Create("test-group", payload)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newRev, err := kv.Update("test-group", payload, rev)
		if err != nil {
			b.Fatal(err)
		}
		rev = newRev
	}
}

// BenchmarkTokenValidation benchmarks the token validation operation.
// This measures the time to validate a fencing token against the KV store.
func BenchmarkTokenValidation(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = election.Stop() }()

	// Wait to become leader
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if election.IsLeader() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	if !election.IsLeader() {
		b.Fatal("Failed to become leader")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = election.ValidateToken(ctx)
	}
}

// BenchmarkIsLeader benchmarks the IsLeader() check operation.
// This measures the overhead of checking leadership status.
func BenchmarkIsLeader(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = election.Stop() }()

	// Wait to become leader
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if election.IsLeader() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = election.IsLeader()
	}
}

// BenchmarkStatus benchmarks the Status() operation.
// This measures the overhead of getting full election status.
func BenchmarkStatus(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = election.Stop() }()

	// Wait to become leader
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if election.IsLeader() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = election.Status()
	}
}

// BenchmarkConcurrentAcquisition benchmarks concurrent leader acquisition attempts.
// This measures the performance when multiple instances compete for leadership.
func BenchmarkConcurrentAcquisition(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			instanceID := "instance-" + time.Now().Format("20060102150405.000000000")
			cfg.InstanceID = instanceID

			adapter := NewMockConnAdapter(nc)
			election, err := NewElection(adapter, cfg)
			if err != nil {
				b.Fatal(err)
			}

			ctx := context.Background()
			if err := election.Start(ctx); err != nil {
				b.Fatal(err)
			}

			// Wait briefly for acquisition attempt
			time.Sleep(10 * time.Millisecond)

			_ = election.Stop()
		}
	})
}

// BenchmarkStateTransition benchmarks state transition operations.
// This measures the overhead of changing election state.
// Note: This benchmark focuses on atomic operations to avoid setup/teardown overhead.
func BenchmarkStateTransition(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	adapter := NewMockConnAdapter(nc)
	kvElection := &kvElection{
		cfg: cfg,
		nc:  adapter,
	}

	// Benchmark atomic state operations (core of state transitions)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kvElection.isLeader.Store(true)
		kvElection.state.Store(StateLeader)
		kvElection.isLeader.Store(false)
		kvElection.state.Store(StateFollower)
	}
}

// BenchmarkPayloadMarshal benchmarks JSON marshaling of leadership payload.
// This measures the overhead of serializing the payload for KV operations.
func BenchmarkPayloadMarshal(b *testing.B) {
	payload := leadershipPayload{
		ID:       "instance-1",
		Token:    "test-token-12345",
		Priority: 10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(payload)
	}
}

// BenchmarkPayloadUnmarshal benchmarks JSON unmarshaling of leadership payload.
// This measures the overhead of deserializing the payload from KV operations.
func BenchmarkPayloadUnmarshal(b *testing.B) {
	payloadBytes := []byte(`{"id":"instance-1","token":"test-token-12345","priority":10}`)
	var payload leadershipPayload

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = json.Unmarshal(payloadBytes, &payload)
	}
}

// BenchmarkLogWithContext benchmarks the logWithContext helper function.
// This measures the overhead of creating log fields.
func BenchmarkLogWithContext(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}

	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	kvElection := election.(*kvElection)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kvElection.logWithContext(ctx)
	}
}

// BenchmarkMetricsRecording benchmarks metrics recording operations.
// This measures the overhead of recording Prometheus metrics.
func BenchmarkMetricsRecording(b *testing.B) {
	nc := natsmock.NewMockConn()
	cfg := ElectionConfig{
		Bucket:            "leaders",
		Group:             "test-group",
		InstanceID:        "instance-1",
		TTL:               10 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		Metrics:           NewPrometheusMetrics(nil), // No-op metrics
	}

	adapter := NewMockConnAdapter(nc)
	election, err := NewElection(adapter, cfg)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = election.Stop() }()

	// Wait to become leader
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if election.IsLeader() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Get labels for metrics
	kvElection := election.(*kvElection)
	labels := kvElection.getMetricsLabels()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg.Metrics.SetIsLeader(1, labels)
		cfg.Metrics.IncTransitions(labels)
		cfg.Metrics.ObserveHeartbeatDuration(10*time.Millisecond, labels)
	}
}

// BenchmarkErrorClassification benchmarks error classification operations.
// This measures the overhead of classifying errors for logging.
func BenchmarkErrorClassification(b *testing.B) {
	err := ErrNotLeader

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyErrorType(err)
		_ = IsPermanentError(err)
		_ = IsTransientError(err)
	}
}
