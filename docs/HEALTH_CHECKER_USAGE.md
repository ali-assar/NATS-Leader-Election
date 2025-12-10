# Health Checker Usage Guide

## Is Health Checker Required?

**NO - Health Checker is OPTIONAL!**

You do **NOT** need to implement the `HealthChecker` interface. If you don't set it (leave it `nil`), health checking is completely disabled and the system works normally.

## When to Use Health Checker

Use health checking when:
- ✅ You have resources that can become unhealthy (database, external APIs, etc.)
- ✅ You want the leader to automatically demote if it becomes unhealthy
- ✅ You want faster failover when the leader has issues

**Don't use health checking when:**
- ❌ You have no resources to check
- ❌ You don't need automatic demotion
- ❌ You want simpler configuration

## Usage Examples

### Example 1: No Health Checker (Simplest)

```go
cfg := ElectionConfig{
    Bucket:            "leaders",
    Group:             "my-group",
    InstanceID:        "instance-1",
    TTL:               10 * time.Second,
    HeartbeatInterval: 1 * time.Second,
    // HealthChecker is nil - health checking is DISABLED
    // This is perfectly fine! System works normally.
}

election, err := NewElection(nc, cfg)
// ... use election normally
```

**Result:** Health checking is disabled. Leader election works normally without any health checks.

---

### Example 2: Simple "Always Healthy" Checker

If you want to enable health checking but don't have specific resources to check, you can create a simple checker that always returns `true`:

```go
type alwaysHealthyChecker struct{}

func (c *alwaysHealthyChecker) Check(ctx context.Context) bool {
    return true // Always healthy
}

cfg := ElectionConfig{
    Bucket:            "leaders",
    Group:             "my-group",
    InstanceID:        "instance-1",
    TTL:               10 * time.Second,
    HeartbeatInterval: 1 * time.Second,
    HealthChecker:     &alwaysHealthyChecker{}, // Always passes
    MaxConsecutiveFailures: 3,
}
```

**Result:** Health checking is enabled but always passes. This is useful if you want the infrastructure in place but don't have specific checks yet.

---

### Example 3: Database Health Checker

If you have a database that the leader needs to access:

```go
type databaseHealthChecker struct {
    db *sql.DB
}

func (c *databaseHealthChecker) Check(ctx context.Context) bool {
    // Quick ping to check if database is accessible
    return c.db.PingContext(ctx) == nil
}

cfg := ElectionConfig{
    Bucket:            "leaders",
    Group:             "my-group",
    InstanceID:        "instance-1",
    TTL:               10 * time.Second,
    HeartbeatInterval: 1 * time.Second,
    HealthChecker:     &databaseHealthChecker{db: myDB},
    MaxConsecutiveFailures: 3, // Demote after 3 consecutive failures
}
```

**Result:** If database becomes unreachable, leader will demote after 3 consecutive failed health checks.

---

### Example 4: Multiple Resource Health Checker

If you need to check multiple resources:

```go
type multiResourceHealthChecker struct {
    db      *sql.DB
    apiURL  string
    cache   *redis.Client
}

func (c *multiResourceHealthChecker) Check(ctx context.Context) bool {
    // Check database
    if err := c.db.PingContext(ctx); err != nil {
        return false
    }
    
    // Check cache (with timeout)
    cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
    defer cancel()
    if err := c.cache.Ping(cacheCtx).Err(); err != nil {
        return false
    }
    
    // Check external API (with timeout)
    apiCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
    defer cancel()
    req, _ := http.NewRequestWithContext(apiCtx, "GET", c.apiURL, nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil || resp.StatusCode != 200 {
        return false
    }
    resp.Body.Close()
    
    return true // All resources healthy
}

cfg := ElectionConfig{
    Bucket:            "leaders",
    Group:             "my-group",
    InstanceID:        "instance-1",
    TTL:               10 * time.Second,
    HeartbeatInterval: 1 * time.Second,
    HealthChecker:     &multiResourceHealthChecker{
        db:     myDB,
        apiURL: "https://api.example.com/health",
        cache:  myRedis,
    },
    MaxConsecutiveFailures: 3,
}
```

**Result:** Leader demotes if any critical resource becomes unhealthy.

---

## Summary

| Scenario | HealthChecker Value | Result |
|----------|-------------------|--------|
| No health checking needed | `nil` | Health checking disabled ✅ |
| No resources to check | `nil` or always-return-true checker | Health checking disabled or always passes ✅ |
| Have resources to check | Implement `HealthChecker` interface | Health checking enabled ✅ |

**Key Points:**
- ✅ **HealthChecker is OPTIONAL** - you don't need to implement it
- ✅ If `nil`, health checking is disabled (default behavior)
- ✅ Only implement it if you want health-based demotion
- ✅ Even with no resources, you can use a simple "always healthy" checker
- ✅ The system works perfectly fine without health checking

