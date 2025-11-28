# Project Plan — nats-leader-election

This document bundles everything you asked for: a project roadmap, a full implementation skeleton (file layout + key code snippets), README examples to copy-paste, and naming/branding + logo suggestions. Treat this as the single source-of-truth for the next phase of work.

---

## 1 — Roadmap (milestones & deliverables)

> Timeline estimates are suggestions for planning only. You control pacing.

### Phase 0 — Kickoff (1–3 days)

* Create repo skeleton, LICENSE, CONTRIBUTING, CODE_OF_CONDUCT
* Add README (already created) and design.md (done)
* Add CI skeleton (GitHub Actions) for `go test` and `golangci-lint`

**Deliverables:** Repo skeleton, CI pipeline

### Phase 1 — Core library (1–2 weeks)

* Implement `Election` interface and `kv` backed implementation
* Heartbeat loop with conditional Update + revision tracking
* Watcher for followers
* OnPromote/OnDemote hooks
* Basic unit tests with mocked `nats.KeyValue`

**Deliverables:** `leader` package, unit tests, API docs

### Phase 2 — Robustness & features (1–2 weeks)

* Fencing tokens fully implemented and tested
* HealthChecker and graceful shutdown logic
* Jitter/backoff strategy for followers
* Configurable logging and Prometheus metrics

**Deliverables:** Improved tests (integration with local nats-server), metrics, health integration

### Phase 3 — Multi-role, examples, docs (1 week)

* `RoleManager` convenience API for multiple roles
* Example apps: simple control manager, multi-role demo, K8s integration example
* README quickstart / examples section updated

**Deliverables:** Example apps, updated README

### Phase 4 — Packaging & outreach (1–2 weeks)

* Release v0.1.0, create GitHub release notes
* Publish a blog post / HN / Reddit announcement + NATS Slack share
* Add example CI for releasing to GitHub packages (optional)

**Deliverables:** v0.1.0 release, announcement materials

---

## 2 — Implementation skeleton (files, packages, and key snippets)

```
nats-leader-election/
├─ cmd/
│  └─ demo/
│     └─ main.go          # demo app to show library usage
├─ leader/
│  ├─ election.go        # public API and types
│  ├─ kv_election.go     # kv-backed implementation
│  ├─ heartbeat.go       # heartbeat logic
│  ├─ watcher.go         # KV watch logic
│  ├─ role_manager.go    # optional multi-role manager
│  ├─ health.go          # health-checker integration
│  ├─ metrics.go         # prometheus metrics
│  └─ errors.go
├─ internal/
│  └─ natsmock/          # test mocks for nats.KeyValue and nats.Conn
├─ examples/
│  ├─ control-manager/   # small app that uses leader
│  └─ multi-role/
├─ scripts/
│  └─ start-nats.sh
├─ .github/
│  └─ workflows/ci.yml
├─ go.mod
├─ README.md
└─ design.md
```

### 2.1 `election.go` (public API)

```go
package leader

import "context"

type Election interface {
    Start(ctx context.Context) error
    Stop() error
    IsLeader() bool
    LeaderID() string
    Token() string
    OnPromote(func(ctx context.Context, token string))
    OnDemote(func())
}

type ElectionConfig struct {
    Bucket            string
    Group             string
    InstanceID        string
    TTL               time.Duration
    HeartbeatInterval time.Duration
    // Optional
    Priority int
    Logger   Logger
}

func NewElection(nc *nats.Conn, cfg ElectionConfig) (Election, error) {
    // returns kv-backed implementation
}
```

### 2.2 `kv_election.go` (core outline)

* Holds `kv nats.KeyValue`, `instanceID`, `token`, `ctx`, `cancel`, `isLeader atomic.Bool`, `rev uint64`
* Implements Start: attempts Create, spawns heartbeat and watcher
* Implements Stop: cancels context, stops heartbeat, optionally deletes key

Key functions to implement:

* `attemptAcquire()` — tries `kv.Create(key, payload)` and sets state on success
* `heartbeatLoop()` — does `kv.Update(key, payload, expectedRevision)` regularly
* `watchLoop()` — waits on `kv.Watch` and signals create attempts on delete

### 2.3 `heartbeat.go`

Pseudocode for heartbeat loop:

```go
func (e *kvElection) heartbeatLoop(ctx context.Context) {
    ticker := time.NewTicker(e.cfg.HeartbeatInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            if !e.IsLeader() { continue }
            // update with expected revision
            rev, err := e.kv.Update(e.groupKey, payload, nats.ExpectedRevision(e.rev))
            if err != nil {
                e.handleHeartbeatFailure(err)
                return
            }
            e.rev = rev
        }
    }
}
```

### 2.4 `watcher.go`

* Use `kv.Watch` to listen for `Delete` events or change events with expired TTL
* On delete → signal `attemptAcquire()` with jitter

### 2.5 `role_manager.go`

High-level convenience type that manages multiple `Election` instances and provides aggregated events (e.g., `OnRolePromote(role, token)`).

---

## 3 — Examples for README (copy/paste)

### Quickstart (short)

````md
### Quickstart

```go
nc, _ := nats.Connect(nats.DefaultURL)

cfg := leader.ElectionConfig{
  Bucket: "leaders",
  Group:  "control-manager",
  InstanceID: "node-01",
  TTL: 5 * time.Second,
  HeartbeatInterval: 1 * time.Second,
}

e, _ := leader.NewElection(nc, cfg)
e.OnPromote(func(ctx context.Context, token string){
  // Start leader-only work
})
e.OnDemote(func(){
  // Stop leader-only work
})

ctx := context.Background()
e.Start(ctx)
````

````

### Multi-role example

```md
// Create multiple roles
roles := []string{"scheduler","cleanup"}
for _, r := range roles {
  cfg.Group = r
  e, _ := leader.NewElection(nc, cfg)
  // register hooks per role
  e.Start(ctx)
}
````

### Graceful shutdown example

```md
// on SIGTERM
_ = e.Stop() // optionally deletes the key so followers can take over fast
```

---