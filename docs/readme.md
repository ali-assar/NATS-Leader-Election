# NATS Leader Election — Go Library

Lightweight, portable, application-level leader election library built on **NATS KeyValue** (JetStream KV which uses RAFT under the hood). Designed to make applications act active/passive or to assign roles/tasks to specific instances in multi-environment deployments (Kubernetes, VMs, edge devices, etc.).

---

## Goal

Provide a minimal, reliable, and production-ready Go library that:

* Elects leaders using NATS KV (no custom RAFT implementation required)
* Supports *application-level* leadership (not restricted to K8s Pods)
* Enables multi-role election (multiple, independent leadership groups)
* Exposes simple callbacks to run leader-specific logic and respond to demotions
* Provides robustness primitives (fencing tokens, TTL heartbeats, health-based demotion)
* Is easy to test, observe, and operate

---

## Why this instead of Kubernetes leader election?

Short summary (details later in this README): 

* **Platform independence** — works on Kubernetes, VMs, bare metal, and edge devices
* **Lower latency** — sub-second heartbeats and fast failover (configurable) vs Kubernetes' typical 10s+ lease timing
* **Multi-role, multi-group elections** — elect multiple leaders for different concerns (scheduler, network, metrics) simultaneously
* **Minimal dependency surface** — just a NATS connection + KV bucket; no Kubernetes API clients or RBAC

---

## High-level design

```
+----------------------+      +----------------------+      +----------------------+
| app instance A       |<---->| NATS (JetStream KV)  |<---->| app instance B       |
| - election client    |      | - KV (leaders/)      |      | - election client    |
| - tasks if leader    |      | - RAFT consensus     |      | - tasks if leader    |
+----------------------+      +----------------------+      +----------------------+
```

Key concepts:

* **KV bucket**: A JetStream KeyValue bucket (e.g., `leaders`) holds keys per election group/role (e.g., `control-manager`, `scheduler`).
* **Key value**: Each election key's value contains `{instance_id, token, meta}` and KV entries are created/updated with TTL (MaxAge) for ephemeral leadership.
* **Create semantics**: A candidate tries `kv.Create(key, value)` and wins only if the key didn't exist (atomic create). If it fails, the candidate is a follower.
* **Heartbeat**: The leader periodically refreshes the key using `kv.Update(..., expectedRevision)` or by re-creating with proper revision semantics to keep TTL alive.
* **Watchers**: Followers watch the key's updates/deletes. When the key disappears (TTL expiry or delete) they attempt to acquire leadership.
* **Fencing token**: Each successful acquisition includes a unique token (UUID). Tasks performed by leader require verifying token validity where necessary to prevent stale leaders acting after demotion.

---

## Core API (recommended)

```go
type Election interface {
    // Start begins participating in the election (runs in background goroutines).
    Start(ctx context.Context) error

    // Stop stops election participation and cleans up background goroutines.
    Stop() error

    // IsLeader returns true if this instance currently holds leadership for the configured group/role.
    IsLeader() bool

    // LeaderID returns the current leader instance ID (may be empty if unknown).
    LeaderID() string

    // Token returns the current fencing token for this instance if leader.
    Token() string

    // OnPromote registers a callback executed when this instance becomes leader.
    OnPromote(func(ctx context.Context, token string))

    // OnDemote registers a callback executed when this instance loses leadership.
    OnDemote(func())
}

func NewElection(nc *nats.Conn, cfg ElectionConfig) (Election, error)
```

`ElectionConfig` contains: `BucketName`, `GroupKey`, `InstanceID`, `HeartbeatInterval`, `TTL`, `Priority` (optional), and `Logger`.

---

## How it works (step-by-step)

### 1) Boot & Attempt

1. Candidate computes value payload `{id: instanceID, token: uuid(), meta: {priority}}`.
2. Candidate calls `kv.Create(groupKey, payload, nats.WithMaxAge(ttl))`.

   * If `Create` succeeds → candidate is leader. Callbacks `OnPromote` run.
   * If `Create` fails (key exists) → candidate becomes follower and starts watching the key.

### 2) Leader heartbeat

* Leader refreshes the key to prevent TTL expiry. There are two common strategies:

  1. `kv.Update(key, payload, expectedRevision)` — ensures the revision matches (prevents blind writes)
  2. Re-create with the same key and new revision when API supports it
* If an update fails due to revision mismatch → leader detects a conflict and demotes itself.

### 3) Follower watch + contest

* Followers watch key events (`kv.Watch(groupKey)`)
* On `Delete` or missing key (TTL expire) followers race to `Create` again
* One of them wins the next election

### 4) Demotion & fencing

* When leadership is lost, the demoted instance stops leader-only tasks.
* Operations that could be unsafe if performed by stale leaders should validate the fencing token against KV before performing effectful actions. Example: leader holds token `T1`. If it loses leadership and token changes to `T2`, any write must fail if token no longer matches.

---

## Important improvements & why they matter

Below, each improvement is explained with *what*, *why*, and *how to implement*.

### 1) Fencing tokens (critical)

**What:** a unique token (UUID) stored with the leader key.

**Why:** prevents a previously-leader instance from continuing to act after it loses leadership (due to network partition or delayed demotion). If leader tasks touch shared resources, they should check token validity before important operations.

**How:** store token in KV value. When performing sensitive operations, verify the token by reading KV and comparing. Optionally, use atomic conditional updates that require the token/revision to match.

### 2) Health-aware demotion

**What:** automatic self-demotion if local health checks fail.

**Why:** if the leader is unhealthy (e.g., can't access DB or is overloaded), it should abdicate so a healthier instance can take control.

**How:** integrate a `HealthChecker` callback that returns `healthy bool`. If it returns false for N consecutive checks, call `kv.Delete(key)` or stop heartbeating and voluntarily demote.

### 3) Multi-role & multi-group support

**What:** allow multiple independent election keys (e.g., `roles/scheduler`, `roles/network`) so different instances can hold different roles simultaneously.

**Why:** many control-plane systems need different exclusive actors for different responsibilities.

**How:** provide convenience helpers to create multiple `Election` objects or a `RoleManager` that manages a map of roles → elections.

### 4) Priority-based takeover (optional)

**What:** candidates can have a `priority` value; higher priority can pre-empt an existing lower-priority leader when certain safe conditions are met.

**Why:** useful when you want to prefer specific nodes (more powerful machines) to be leaders.

**How:** store `priority` in the KV value. On watcher events, if a candidate sees a lower priority leader AND the leader's token/revision shows inactivity or a manual override flag, implement a controlled takeover (careful — risk of split-brain; prefer explicit demotion).

### 5) Observability & metrics

**What:** Prometheus metrics such as `election_is_leader`, `election_transitions_total`, `election_failures_total`.

**Why:** operators need to know who's leader, how often elections occur, and whether something is wrong.

**How:** export metrics via Prometheus client and document labels: `role`, `instance_id`, and `bucket`.

### 6) Graceful shutdown & readiness

**What:** on SIGTERM, leader should optionally release leadership or mark itself as not ready and stop heartbeating to allow quick failover.

**Why:** reduce downtime during rolling upgrades.

**How:** provide `Shutdown(ctx)` that deletes the KV key or stops heartbeating and waits for `OnDemote` to finish.

### 7) gRPC task distribution (optional advanced)

**What:** let leader become an instruction source via a small gRPC control plane — followers subscribe and receive tasks.

**Why:** for complex orchestration, letting leader push assignments is easier than polling.

**How:** the library can provide a small `control` package to publish task messages to a subject or via gRPC with leader authentication using token.

---

## Usage example

```go
nc, _ := nats.Connect(nats.DefaultURL)
js, _ := nc.JetStream()
kv, _ := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "leaders"})

cfg := leader.ElectionConfig{
    Bucket: "leaders",
    Group:  "control-manager",
    InstanceID: myID,
    TTL: 5 * time.Second,
    HeartbeatInterval: 1 * time.Second,
}

e, err := leader.NewElection(nc, cfg)
if err != nil { log.Fatal(err) }

// register hooks
e.OnPromote(func(ctx context.Context, token string) {
    log.Println("promoted — starting leader tasks")
    go startLeaderTasks(ctx, token)
})

e.OnDemote(func() {
    log.Println("demoted — stopping leader tasks")
    stopLeaderTasks()
})

ctx := context.Background()
if err := e.Start(ctx); err != nil { log.Fatal(err) }

// run until shutdown
<-ctx.Done()
_ = e.Stop()
```

---

## Edge cases & pitfalls

* **Clock skew**: Don't rely on system clocks for ordering; use KV revision and tokens.
* **Network partitions**: Because NATS KV is RAFT-based, it tolerates partitions; but be cautious — if your client loses connectivity but remains running, ensure it demotes on lost heartbeats.
* **Split-brain**: Using `Create` + `Update` with expected revisions and fencing tokens greatly reduces this risk. Do not implement a takeover by `Put` without revision checks.
* **Thundering herd**: When a leader key expires, many followers may attempt `Create` at once. Use jittered backoff when retrying to reduce load.
* **Unreliable KV TTL semantics**: Test TTL and heartbeat timing under load. Make heartbeat interval significantly smaller than TTL and consider performing a `kv.Update` before TTL/2.

---

## Testing

* Unit tests should mock `nats.Conn` and `nats.KeyValue` interfaces. Provide test helpers that simulate KV state transitions (Create success/fail, Update success/fail, Watch events).
* Integration tests should run a local nats-server (JetStream enabled) with a KV bucket and exercise leader takeover, demotion, and fencing.
* Chaos tests: simulate network drops, process stalls, and verify safe failover.

---

## Comparison matrix — NATS KV election vs Kubernetes Lease

| Feature                |          NATS KV (this lib) | Kubernetes Lease                      |
| ---------------------- | --------------------------: | ------------------------------------- |
| Platform portability   |            ✅ works anywhere | ❌ requires K8s API server             |
| Failover latency       | ✅ sub-second (configurable) | ❌ often 10s+ by default               |
| Multi-role support     |      ✅ easy (multiple keys) | ❌ single Lease per use-case           |
| Fencing tokens         |                 ✅ supported | ❌ not standard                        |
| Operational complexity |  ✅ single dependency (NATS) | ❌ kube-client + RBAC + cluster access |

---

## Roadmap / Future work

* CLI tool for debugging elections (list leaders, force-demote, inspect tokens)
* Official Prometheus exporter with advanced metrics
* `RoleManager` that manages many roles with single client connection
* gRPC control-plane scaffolding for task assignment
* Example operators for Kubernetes to show how to integrate with Pods when running inside cluster
* Optional integration with service discovery (consul / etcd) for multi-datacenter awareness

---

## Security considerations

* Use TLS between clients and NATS server
* Use NATS accounts/credentials to restrict who can write to KV buckets
* Treat fencing tokens as short-lived secrets; avoid logging them
* Provide signed tokens for stronger guarantees (optional)

---

## Contributing

* Open an issue for feature requests or bugs
* Add unit tests for new behaviors
* Keep API changes backward compatible; bump major version for breaking changes

---

## License

Choose a permissive license (e.g., MIT or Apache 2.0) and include `LICENSE` in the repo.

---

## Appendix — Example KV payload (JSON)

```json
{
  "id": "instance-1234",
  "token": "3f9a6f8d-1a2b-4cde-9f1a-c0b123456789",
  "priority": 10,
  "meta": {"hostname": "node-01", "region": "eu-west-1"}
}
```

---

If you'd like, I can now:

1. Generate the first working Go implementation (core features + tests)
2. Produce a smaller `README` focused on quickstart and API usage
3. Create a `design.md` with sequence diagrams and state diagrams in more detail

Pick one and I will produce the code or doc next.
