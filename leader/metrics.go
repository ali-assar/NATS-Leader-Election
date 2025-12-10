package leader

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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
// This is a placeholder - full implementation will be added later.
// For now, returns a no-op implementation.
func NewPrometheusMetrics(registry prometheus.Registerer) Metrics {
	// TODO: Implement full Prometheus metrics
	return &noOpMetrics{}
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
