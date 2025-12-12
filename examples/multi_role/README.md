# Multi-Role Leader Election Example

This example demonstrates how to use multiple independent leader elections for different roles in a single application instance.

## What is Multi-Role Election?

Multi-role election allows a single application instance to participate in **multiple independent leader elections** simultaneously. Each role has its own leader, and different instances can be leaders of different roles at the same time.

### Key Concept

**One instance can be leader of multiple roles, and different instances can lead different roles simultaneously.**

## Example Scenario

Imagine you have a distributed system with 3 instances (A, B, C) and 3 roles:

### Role 1: `database-writer`
- **Purpose**: Only one instance should write to the database
- **Leader**: Instance A
- **Followers**: Instance B, Instance C

### Role 2: `scheduler`
- **Purpose**: Only one instance should run scheduled tasks
- **Leader**: Instance B
- **Followers**: Instance A, Instance C

### Role 3: `metrics-collector`
- **Purpose**: Only one instance should aggregate metrics
- **Leader**: Instance C
- **Followers**: Instance A, Instance B

**Result**: Each instance is leader of one role and follower of the other two roles.

## How It Works

### 1. Multiple Independent Elections

Each role uses a **different `Group` name** in the election configuration:

```go
// Role 1: database-writer
cfg1 := leader.ElectionConfig{
    Bucket: "leaders",           // Same bucket
    Group:  "database-writer",   // Different group = different role
    InstanceID: "instance-A",
    // ...
}

// Role 2: scheduler
cfg2 := leader.ElectionConfig{
    Bucket: "leaders",           // Same bucket
    Group:  "scheduler",         // Different group = different role
    InstanceID: "instance-A",
    // ...
}

// Role 3: metrics-collector
cfg3 := leader.ElectionConfig{
    Bucket: "leaders",           // Same bucket
    Group:  "metrics-collector", // Different group = different role
    InstanceID: "instance-A",
    // ...
}
```

### 2. Independent Leadership

- Each `Group` has its own key in the KV store
- Each `Group` has its own leader election
- Leadership in one role does not affect other roles
- An instance can be leader of multiple roles simultaneously

### 3. Role-Specific Callbacks

Each election has its own callbacks:

```go
// Database writer election
dbElection.OnPromote(func(ctx context.Context, token string) {
    // Start database write operations
    startDatabaseWriter(ctx, token)
})

// Scheduler election
schedulerElection.OnPromote(func(ctx context.Context, token string) {
    // Start task scheduling
    startScheduler(ctx, token)
})

// Metrics collector election
metricsElection.OnPromote(func(ctx context.Context, token string) {
    // Start metrics aggregation
    startMetricsCollector(ctx, token)
})
```

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    NATS KV Bucket: "leaders"                 │
├─────────────────────────────────────────────────────────────┤
│  Key: "database-writer"  →  Value: {leader: "instance-A"}   │
│  Key: "scheduler"        →  Value: {leader: "instance-B"}   │
│  Key: "metrics-collector" → Value: {leader: "instance-C"}   │
└─────────────────────────────────────────────────────────────┘
                            ▲
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
┌───────┴──────┐   ┌────────┴────────┐   ┌─────┴──────┐
│ Instance A   │   │  Instance B     │   │ Instance C │
├──────────────┤   ├─────────────────┤   ├────────────┤
│ Role 1: ✓    │   │ Role 1: ✗       │   │ Role 1: ✗  │
│ Role 2: ✗    │   │ Role 2: ✓       │   │ Role 2: ✗  │
│ Role 3: ✗    │   │ Role 3: ✗       │   │ Role 3: ✓  │
└──────────────┘   └─────────────────┘   └────────────┘
  (Leader)          (Leader)              (Leader)
```

## Code Structure

### RoleManager

The `RoleManager` struct helps manage multiple elections:

```go
type RoleManager struct {
    elections map[string]leader.Election  // Maps role name to election
    mu        sync.RWMutex                // Thread-safe access
}
```

**Methods:**
- `AddRole(roleName, election)`: Add a role election to the manager
- `GetRoleStatus()`: Get status of all roles (which ones this instance leads)
- `PrintStatus()`: Print current status of all roles

### Creating Multiple Elections

```go
roles := []string{
    "database-writer",
    "scheduler",
    "metrics-collector",
}

for _, role := range roles {
    // Create election config for this role
    cfg := leader.ElectionConfig{
        Bucket:  "leaders",
        Group:   role,  // Different group = different role
        // ...
    }
    
    // Create and start election
    election, _ := leader.NewElectionWithConn(nc, cfg)
    election.Start(ctx)
    
    // Register role-specific callbacks
    election.OnPromote(func(ctx context.Context, token string) {
        // Start role-specific tasks
    })
}
```

## Running the Example

### Start Multiple Instances

```bash
# Terminal 1
go run examples/multi_role/main.go

# Terminal 2 (different instance ID)
INSTANCE_ID=instance-2 go run examples/multi_role/main.go

# Terminal 3 (different instance ID)
INSTANCE_ID=instance-3 go run examples/multi_role/main.go
```

### Expected Behavior

1. **Initial State**: All instances start and compete for each role
2. **Election Result**: Each instance becomes leader of one role
3. **Status Output**: Each instance prints which roles it leads
4. **Failover**: If an instance stops, other instances compete to take over its role

### Example Output

```
Instance A:
 Role Status:
  ✓ database-writer: LEADER
  ○ scheduler: FOLLOWER (Leader: instance-2)
  ○ metrics-collector: FOLLOWER (Leader: instance-3)

Instance B:
 Role Status:
  ○ database-writer: FOLLOWER (Leader: instance-1)
  ✓ scheduler: LEADER
  ○ metrics-collector: FOLLOWER (Leader: instance-3)

Instance C:
 Role Status:
  ○ database-writer: FOLLOWER (Leader: instance-1)
  ○ scheduler: FOLLOWER (Leader: instance-2)
  ✓ metrics-collector: LEADER
```

## Use Cases

### 1. Separation of Concerns

Different responsibilities require different leaders:
- **Database Writer**: Only one instance writes to avoid conflicts
- **Task Scheduler**: Only one instance schedules tasks
- **Metrics Aggregator**: Only one instance aggregates metrics

### 2. Load Distribution

Spread leadership across instances:
- Instance A handles database operations
- Instance B handles scheduling
- Instance C handles metrics
- All instances share the load

### 3. Independent Failover

Each role fails over independently:
- If database writer fails, only that role re-elects
- Scheduler and metrics collector continue normally
- No impact on other roles

## Important Notes

### Same Bucket, Different Groups

- All roles use the **same KV bucket** (`"leaders"`)
- Each role uses a **different group name** (the key in the bucket)
- This is efficient: one bucket, multiple keys

### Independent Elections

- Each role election is **completely independent**
- Leadership in one role does not affect others
- Each role has its own:
  - Heartbeat loop
  - Watcher
  - Callbacks
  - State machine

### Resource Usage

- Each election uses:
  - One goroutine for heartbeat (if leader)
  - One goroutine for watcher (if follower)
  - One goroutine for validation (if leader and ValidationInterval > 0)
- With 3 roles, you have 3 independent elections running

## Comparison: Single Role vs Multi-Role

### Single Role (Traditional)
```
Instance A: Leader of "control-manager"
Instance B: Follower of "control-manager"
Instance C: Follower of "control-manager"
```
- Only one instance does all leader work
- Other instances are idle (followers)

### Multi-Role (This Example)
```
Instance A: Leader of "database-writer", Follower of others
Instance B: Leader of "scheduler", Follower of others
Instance C: Leader of "metrics-collector", Follower of others
```
- All instances share the work
- Each instance leads at least one role
- Better resource utilization

## Extending the Example

### Add More Roles

Simply add to the `roles` slice:

```go
roles := []string{
    "database-writer",
    "scheduler",
    "metrics-collector",
    "network-coordinator",  // New role
    "cache-manager",        // New role
}
```

### Role-Specific Tasks

In the `OnPromote` callback, start role-specific work:

```go
election.OnPromote(func(ctx context.Context, token string) {
    switch roleName {
    case "database-writer":
        go startDatabaseWriter(ctx, token)
    case "scheduler":
        go startTaskScheduler(ctx, token)
    case "metrics-collector":
        go startMetricsAggregator(ctx, token)
    }
})
```

### Health Checks per Role

You can add health checks specific to each role:

```go
cfg := leader.ElectionConfig{
    // ...
    HealthChecker: &databaseHealthChecker{},  // For database-writer
    // ...
}
```

## Troubleshooting

### Issue: All instances become leader of same role

**Cause**: All instances have the same `InstanceID`
**Solution**: Ensure each instance has a unique `InstanceID` (use environment variable or hostname+PID)

### Issue: Roles not distributing evenly

**Cause**: This is normal - leadership is determined by who wins the race
**Solution**: The system will balance over time as instances restart or fail

### Issue: High resource usage

**Cause**: Multiple elections = multiple goroutines
**Solution**: This is expected. Each role needs its own election. Consider reducing number of roles if needed.

## Next Steps

- Try running with different numbers of instances
- Stop one instance and watch failover
- Add more roles to see how they distribute
- Implement actual role-specific tasks in the callbacks
