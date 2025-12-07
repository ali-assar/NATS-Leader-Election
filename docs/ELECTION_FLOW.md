# Leader Election Flow Diagrams

## State Machine Flow

```
┌─────────┐
│  INIT   │
└────┬────┘
     │ Start()
     ▼
┌─────────────┐
│  CANDIDATE  │
└─────┬───────┘
      │
      ├─ attemptAcquire() succeeds
      │  └─► ┌──────────┐
      │      │  LEADER  │
      │      └────┬─────┘
      │           │
      │           ├─ Heartbeat fails
      │           │  └─► ┌───────────┐
      │           │      │  FOLLOWER │
      │           │      └─────┬─────┘
      │           │            │
      │           │            ├─ Key deleted/nil
      │           │            │  └─► ┌─────────────┐
      │           │            │      │  CANDIDATE  │ (re-election)
      │           │            │      └─────┬───────┘
      │           │            │            │
      │           │            └─ Leader changes
      │           │               └─► Update leaderID
      │           │
      │           └─ Stop()
      │              └─► ┌──────────┐
      │                  │ STOPPED  │
      │                  └──────────┘
      │
      └─ attemptAcquire() fails
         └─► ┌───────────┐
             │  FOLLOWER │
             └───────────┘
```

## Goroutine Lifecycle

### Leader State
```
Start()
  │
  ├─► [Goroutine 1] attemptAcquire()
  │     │
  │     └─► becomeLeader()
  │           │
  │           ├─► [Goroutine 2] heartbeatLoop()
  │           │     │
  │           │     └─► Ticker every HeartbeatInterval
  │           │           │
  │           │           ├─► Update KV (success) ──┐
  │           │           │                         │
  │           │           └─► Update KV (fail)      │
  │           │                 │                    │
  │           │                 └─► handleHeartbeatFailure()
  │           │                       │
  │           │                       └─► becomeFollower()
  │           │
  │           └─► [Goroutine 3] onPromote callback
  │                 │
  │                 └─► (with panic recovery)
  │
  └─► [Main] Wait for result
```

### Follower State
```
becomeFollower()
  │
  └─► [Goroutine] watchLoop()
        │
        ├─► Watch KV key
        │
        └─► for {
              │
              ├─► ctx.Done() ──► Exit
              │
              └─► entry from watcher.Updates()
                    │
                    ├─► entry == nil
                    │   └─► [New Goroutine] attemptAcquireWithRetry()
                    │         │
                    │         └─► (with jitter + backoff)
                    │
                    ├─► entry.Value() == empty
                    │   └─► [New Goroutine] attemptAcquireWithRetry()
                    │
                    └─► Valid entry
                          │
                          ├─► Update leaderID
                          │
                          └─► Update revision
```

## Concurrency Control

### Mutex Protection
```
┌─────────────────────────────────────┐
│  becomeLeader()                     │
│  ┌───────────────────────────────┐  │
│  │ mu.Lock()                     │  │
│  │   ├─► Update atomic values    │  │
│  │   ├─► Start heartbeat goroutine│  │
│  │   └─► Start callback goroutine │  │
│  │ mu.Unlock()                   │  │
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘

┌─────────────────────────────────────┐
│  IsLeader()                         │
│  ┌───────────────────────────────┐  │
│  │ return isLeader.Load()        │  │  ← Lock-free read!
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘
```

### WaitGroup Tracking
```
Start()
  │
  ├─► wg.Add(1) ──┐
  │                │
  │                └─► [Goroutine] attemptAcquire()
  │                      │
  │                      └─► wg.Done() ──┐
  │                                       │
becomeLeader()                            │
  │                                       │
  ├─► wg.Add(1) ──┐                      │
  │                │                      │
  │                └─► [Goroutine] heartbeatLoop()
  │                      │                │
  │                      └─► wg.Done() ──┤
  │                                       │
  └─► wg.Add(1) ──┐                      │
                   │                      │
                   └─► [Goroutine] onPromote()
                         │                │
                         └─► wg.Done() ──┤
                                          │
Stop()                                    │
  │                                       │
  ├─► cancel() ──► Signal all goroutines │
  │                                       │
  └─► wg.Wait() ─────────────────────────┘
         │
         └─► Wait for all wg.Done() calls
```

## Error Handling Flow

### Heartbeat Failure
```
heartbeatLoop()
  │
  ├─► Update KV
  │     │
  │     ├─► Success ──► Continue
  │     │
  │     └─► Error
  │           │
  │           ├─► isPermanentError()?
  │           │     │
  │           │     ├─► Yes ──► handleHeartbeatFailure()
  │           │     │            │
  │           │     │            ├─► becomeFollower()
  │           │     │            │
  │           │     │            └─► onDemote callback
  │           │     │
  │           │     └─► No ──► Retry (up to maxFailures)
  │           │                 │
  │           │                 └─► After maxFailures ──► handleHeartbeatFailure()
  │           │
  │           └─► Timeout ──► handleHeartbeatFailure()
```

### Re-election Flow
```
watchLoop()
  │
  └─► handleWatchEvent(entry)
        │
        ├─► entry == nil ──► attemptAcquireWithRetry()
        │                      │
        │                      ├─► Jitter (10-100ms)
        │                      │
        │                      └─► Retry loop (max 3 retries)
        │                            │
        │                            ├─► attemptAcquire() succeeds
        │                            │   └─► becomeLeader()
        │                            │
        │                            └─► All retries fail
        │                                  └─► becomeFollower()
        │
        └─► Valid entry
              │
              ├─► Update leaderID
              │
              └─► Update revision
```

## Channel Communication

### Watcher Updates
```
┌──────────────┐
│  NATS KV     │
│  (Real/Mock) │
└──────┬───────┘
       │
       │ Updates
       ▼
┌──────────────┐
│  Watcher     │
│  Updates()   │──► chan Entry
└──────┬───────┘
       │
       ▼
┌──────────────┐
│ watchLoop()  │
│              │
│  select {    │
│    case entry│
│    case ctx  │
│  }           │
└──────┬───────┘
       │
       ▼
┌──────────────┐
│handleWatch   │
│Event()       │
└──────────────┘
```

### Heartbeat Timeout
```
┌──────────────┐
│heartbeatLoop │
└──────┬───────┘
       │
       ├─► [Goroutine] Update KV
       │     │
       │     └─► resultChan ──┐
       │                       │
       └─► select {            │
             │                 │
             ├─► ctx.Done()    │
             │                 │
             ├─► time.After()  │  ← Timeout
             │                 │
             └─► resultChan ───┘  ← Success
```

## Stop Sequence

```
Stop()
  │
  ├─► mu.Lock()
  │     │
  │     ├─► Check if already stopped
  │     │
  │     ├─► wasLeader = isLeader.Load()
  │     │
  │     ├─► cancel() ──► Signal all goroutines
  │     │
  │     ├─► Update state to STOPPED
  │     │
  │     └─► mu.Unlock()
  │
  ├─► [Goroutine] wg.Wait()
  │     │
  │     └─► Blocks until all goroutines done
  │
  ├─► select {
  │     │
  │     ├─► <-done ──► All goroutines finished
  │     │
  │     └─► <-time.After(5s) ──► Timeout
  │
  └─► if wasLeader && onDemote != nil
        │
        └─► onDemote() callback
```

## Multiple Instances Competing

```
Time ───────────────────────────────────────────►

Instance 1:  [Start]──►[Candidate]──►[Leader]───────────►[Heartbeat]──►[Heartbeat]──►
                                                              │
Instance 2:        [Start]──►[Candidate]──►[Follower]────────┼──►[Watch]───────────────►
                                                              │
Instance 3:              [Start]──►[Candidate]──►[Follower]──┼──►[Watch]───────────────►
                                                              │
                                                              │ Leader crashes
                                                              ▼
Instance 1:  ────────────────────────────────────────────────────►[STOPPED]
                                                              │
Instance 2:  ────────────────────────────────────────────────────►[Watch detects nil]──►[Candidate]──►[Leader]──►
                                                              │
Instance 3:  ────────────────────────────────────────────────────►[Watch detects nil]──►[Candidate]──►[Follower]──►
```

## Key Design Decisions

### Why Atomic for Reads?
```
High-frequency reads (IsLeader, LeaderID, Token)
  │
  ├─► Option 1: Mutex
  │     │
  │     └─► Every read acquires lock (slow)
  │
  └─► Option 2: Atomic ✅
        │
        └─► Lock-free reads (fast)
              │
              └─► Writes protected by mutex (rare)
```

### Why Separate Goroutines?
```
becomeLeader()
  │
  ├─► Heartbeat goroutine
  │     │
  │     └─► Can't block on network I/O
  │
  ├─► Callback goroutine
  │     │
  │     └─► User code might be slow/blocking
  │
  └─► Main goroutine
        │
        └─► Returns immediately (non-blocking)
```

### Why Context Cancellation?
```
Stop()
  │
  └─► cancel() ──► ctx.Done() closes
        │
        ├─► heartbeatLoop() checks ctx.Done() ──► Exit
        │
        ├─► watchLoop() checks ctx.Done() ──► Exit
        │
        └─► All goroutines exit gracefully
```

---

**Visual Legend:**
- `[State]` = State machine state
- `[Goroutine]` = Running goroutine
- `─►` = Flow direction
- `│` = Continuation
- `└─►` = Branch
- `├─►` = Branch continuation


