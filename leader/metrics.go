package leader

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics defines the interface for recording metrics.
// All metrics are optional - if nil, metrics recording is disabled.
type Metrics interface {
	// Gauges
	SetIsLeader(value float64, labels prometheus.Labels)
	SetConnectionStatus(value float64, labels prometheus.Labels)

	// Counters
	IncTransitions(labels prometheus.Labels)
	IncFailures(labels prometheus.Labels)
	IncAcquireAttempts(labels prometheus.Labels)
	IncTokenValidationFailures(labels prometheus.Labels)

	// Histograms
	ObserveHeartbeatDuration(duration time.Duration, labels prometheus.Labels)
	ObserveLeaderDuration(duration time.Duration, labels prometheus.Labels)
}

type prometheusMetrics struct {
	isLeader                *prometheus.GaugeVec
	connectionStatus        *prometheus.GaugeVec
	transitionsTotal        *prometheus.CounterVec
	failuresTotal           *prometheus.CounterVec
	acquireAttemptsTotal    *prometheus.CounterVec
	tokenValidationFailures *prometheus.CounterVec
	heartbeatDuration       *prometheus.HistogramVec
	leaderDuration          *prometheus.HistogramVec
}

// NewPrometheusMetrics creates a new Prometheus metrics implementation.
// If registry is nil, returns a no-op implementation.
func NewPrometheusMetrics(registry prometheus.Registerer) Metrics {
	if registry == nil {
		return &noOpMetrics{}
	}

	m := &prometheusMetrics{
		isLeader: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "election_is_leader",
				Help: "1 if current instance is leader, 0 otherwise",
			},
			[]string{"role", "instance_id", "bucket"},
		),
		connectionStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "election_connection_status",
				Help: "NATS connection status (1=connected, 0=disconnected)",
			},
			[]string{"role", "instance_id", "bucket"},
		),
		transitionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "election_transitions_total",
				Help: "Total number of state transitions",
			},
			[]string{"role", "instance_id", "bucket", "from_state", "to_state"},
		),
		failuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "election_failures_total",
				Help: "Total number of election failures",
			},
			[]string{"role", "instance_id", "bucket", "error_type"},
		),
		acquireAttemptsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "election_acquire_attempts_total",
				Help: "Total election acquisition attempts",
			},
			[]string{"role", "instance_id", "bucket", "status"},
		),
		tokenValidationFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "election_token_validation_failures_total",
				Help: "Token validation failures (indicates stale leaders)",
			},
			[]string{"role", "instance_id", "bucket"},
		),
		heartbeatDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "election_heartbeat_duration_seconds",
				Help:    "Time taken for heartbeat operations",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
			},
			[]string{"role", "instance_id", "bucket", "status"},
		),
		leaderDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "election_leader_duration_seconds",
				Help:    "How long instances hold leadership",
				Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~1h
			},
			[]string{"role", "instance_id", "bucket"},
		),
	}

	// Register all metrics with the registry
	registry.MustRegister(
		m.isLeader,
		m.connectionStatus,
		m.transitionsTotal,
		m.failuresTotal,
		m.acquireAttemptsTotal,
		m.tokenValidationFailures,
		m.heartbeatDuration,
		m.leaderDuration,
	)

	return m
}

// SetIsLeader sets the is_leader gauge metric
func (m *prometheusMetrics) SetIsLeader(value float64, labels prometheus.Labels) {
	m.isLeader.With(labels).Set(value)
}

// SetConnectionStatus sets the connection_status gauge metric
func (m *prometheusMetrics) SetConnectionStatus(value float64, labels prometheus.Labels) {
	m.connectionStatus.With(labels).Set(value)
}

// IncTransitions increments the transitions_total counter
func (m *prometheusMetrics) IncTransitions(labels prometheus.Labels) {
	m.transitionsTotal.With(labels).Inc()
}

// IncFailures increments the failures_total counter
func (m *prometheusMetrics) IncFailures(labels prometheus.Labels) {
	m.failuresTotal.With(labels).Inc()
}

// IncAcquireAttempts increments the acquire_attempts_total counter
func (m *prometheusMetrics) IncAcquireAttempts(labels prometheus.Labels) {
	m.acquireAttemptsTotal.With(labels).Inc()
}

// IncTokenValidationFailures increments the token_validation_failures_total counter
func (m *prometheusMetrics) IncTokenValidationFailures(labels prometheus.Labels) {
	m.tokenValidationFailures.With(labels).Inc()
}

// ObserveHeartbeatDuration records the heartbeat_duration_seconds histogram
func (m *prometheusMetrics) ObserveHeartbeatDuration(duration time.Duration, labels prometheus.Labels) {
	m.heartbeatDuration.With(labels).Observe(duration.Seconds())
}

// ObserveLeaderDuration records the leader_duration_seconds histogram
func (m *prometheusMetrics) ObserveLeaderDuration(duration time.Duration, labels prometheus.Labels) {
	m.leaderDuration.With(labels).Observe(duration.Seconds())
}

// noOpMetrics is a no-op implementation of Metrics
type noOpMetrics struct{}

func (n *noOpMetrics) SetIsLeader(value float64, labels prometheus.Labels)                       {}
func (n *noOpMetrics) SetConnectionStatus(value float64, labels prometheus.Labels)               {}
func (n *noOpMetrics) IncTransitions(labels prometheus.Labels)                                   {}
func (n *noOpMetrics) IncFailures(labels prometheus.Labels)                                      {}
func (n *noOpMetrics) IncAcquireAttempts(labels prometheus.Labels)                               {}
func (n *noOpMetrics) IncTokenValidationFailures(labels prometheus.Labels)                       {}
func (n *noOpMetrics) ObserveHeartbeatDuration(duration time.Duration, labels prometheus.Labels) {}
func (n *noOpMetrics) ObserveLeaderDuration(duration time.Duration, labels prometheus.Labels)    {}
