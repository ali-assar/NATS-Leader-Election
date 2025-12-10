package leader

import (
	"context"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/utils/logger"
	"github.com/nats-io/nats.go"
)

type Election interface {
	Start(ctx context.Context) error
	Stop() error
	StopWithContext(ctx context.Context, opts StopOptions) error
	Status() ElectionStatus
	IsLeader() bool
	LeaderID() string
	Token() string
	OnPromote(func(ctx context.Context, token string))
	OnDemote(func())

	ValidateToken(ctx context.Context) (bool, error)
	ValidateTokenOrDemote(ctx context.Context) bool
}

type ElectionConfig struct {
	Bucket                string
	Group                 string
	InstanceID            string
	TTL                   time.Duration
	HeartbeatInterval     time.Duration
	Logger                logger.Logger
	ValidationInterval    time.Duration
	DisconnectGracePeriod time.Duration //if zero, uses default (3x HeartbeatInterval, minimum 5 seconds).

	// Health checking (OPTIONAL)
	// If HealthChecker is nil, health checking is disabled.
	// If provided, health is checked before each heartbeat.
	// If health check fails MaxConsecutiveFailures times consecutively, leader demotes.
	HealthChecker          HealthChecker // Optional: nil = disabled, non-nil = enabled
	MaxConsecutiveFailures int           // Max consecutive failures before demotion (default: 3, only used if HealthChecker is set)
}

// StopOptions configures the behavior of StopWithContext.
type StopOptions struct {
	// DeleteKey deletes the leadership key on stop for fast failover.
	// If true, the key is deleted immediately, allowing a new leader
	// to be elected without waiting for TTL expiration.
	DeleteKey bool

	// WaitForDemote waits for the OnDemote callback to complete before
	// returning. If false, the callback is called but not waited for.
	WaitForDemote bool

	// Timeout is the maximum time to wait for shutdown operations.
	// If 0, the context timeout is used. If context has no timeout,
	// a default of 5 seconds is used.
	Timeout time.Duration
}

type JetStreamProvider interface {
	JetStream() (JetStreamContext, error)
}

// NATSConnectionProvider provides access to the underlying NATS connection
// for connection monitoring. This is optional - if not implemented,
// connection monitoring will be disabled.
type NATSConnectionProvider interface {
	NATSConnection() *nats.Conn
}

type Entry interface {
	Key() string
	Value() []byte
	Revision() uint64
}

type Watcher interface {
	Updates() <-chan Entry
	Stop()
}

type KeyValue interface {
	Create(key string, value []byte, opts ...interface{}) (uint64, error)
	Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error)
	Get(key string) (Entry, error)
	Delete(key string) error
	Watch(key string, opts ...interface{}) (Watcher, error)
}

type JetStreamContext interface {
	KeyValue(bucket string) (KeyValue, error)
}

type natsKeyValueAdapter struct {
	kv nats.KeyValue
}

func (a *natsKeyValueAdapter) Create(key string, value []byte, opts ...interface{}) (uint64, error) {
	// Note: NATS KV doesn't support per-key TTL options.
	// TTL is set at the bucket level when creating the bucket.
	// We accept TTL in opts for interface compatibility, but ignore it here.
	// The mock implementation can track TTL if needed for testing.
	return a.kv.Create(key, value)
}

func (a *natsKeyValueAdapter) Update(key string, value []byte, rev uint64, opts ...interface{}) (uint64, error) {
	// Note: NATS KV doesn't support per-key TTL options.
	// TTL is set at the bucket level when creating the bucket.
	// We accept TTL in opts for interface compatibility, but ignore it here.
	// The mock implementation can track TTL if needed for testing.
	return a.kv.Update(key, value, rev)
}

func (a *natsKeyValueAdapter) Get(key string) (Entry, error) {
	natsEntry, err := a.kv.Get(key)
	if err != nil {
		return nil, err
	}
	if natsEntry == nil {
		return nil, nil
	}
	return &natsEntryAdapter{entry: natsEntry}, nil
}

func (a *natsKeyValueAdapter) Delete(key string) error {
	return a.kv.Delete(key)
}

func (a *natsKeyValueAdapter) Watch(key string, opts ...interface{}) (Watcher, error) {
	natsWatcher, err := a.kv.Watch(key)
	if err != nil {
		return nil, err
	}
	return &natsWatcherAdapter{watcher: natsWatcher}, nil
}

type natsWatcherAdapter struct {
	watcher nats.KeyWatcher
}

func (a *natsWatcherAdapter) Updates() <-chan Entry {
	entryChan := make(chan Entry, 1)
	go func() {
		defer close(entryChan)
		for natsEntry := range a.watcher.Updates() {
			if natsEntry != nil {
				entryChan <- &natsEntryAdapter{entry: natsEntry}
			} else {
				entryChan <- nil
			}
		}
	}()
	return entryChan
}

func (a *natsWatcherAdapter) Stop() {
	a.watcher.Stop()
}

type natsEntryAdapter struct {
	entry nats.KeyValueEntry
}

func (a *natsEntryAdapter) Key() string {
	return a.entry.Key()
}

func (a *natsEntryAdapter) Value() []byte {
	return a.entry.Value()
}

func (a *natsEntryAdapter) Revision() uint64 {
	return a.entry.Revision()
}

type natsJetStreamAdapter struct {
	js nats.JetStreamContext
}

func (a *natsJetStreamAdapter) KeyValue(bucket string) (KeyValue, error) {
	kv, err := a.js.KeyValue(bucket)
	if err != nil {
		return nil, err
	}
	return &natsKeyValueAdapter{kv: kv}, nil
}

type natsConnAdapter struct {
	nc *nats.Conn
}

func (a *natsConnAdapter) JetStream() (JetStreamContext, error) {
	js, err := a.nc.JetStream()
	if err != nil {
		return nil, err
	}
	return &natsJetStreamAdapter{js: js}, nil
}

func NewElection(nc JetStreamProvider, cfg ElectionConfig) (Election, error) {
	return newKVElection(nc, cfg)
}

func NewElectionWithConn(nc *nats.Conn, cfg ElectionConfig) (Election, error) {
	return NewElection(&natsConnAdapter{nc: nc}, cfg)
}

type ElectionStatus struct {
	State          string
	IsLeader       bool
	LeaderID       string
	Token          string
	LastHeartbeat  time.Time
	LastTransition time.Time
	Revision       uint64
}
