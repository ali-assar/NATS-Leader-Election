# Examples

This directory contains example applications demonstrating how to use the NATS Leader Election library in different scenarios.

## Prerequisites

1. **NATS Server**: You need a running NATS server with JetStream enabled.
   - **Option 1**: Use the default embedded server (for testing)
   - **Option 2**: Run a local NATS server:
     ```bash
     docker run -p 4222:4222 -ti nats:latest -js
     ```
   - **Option 3**: Use an existing NATS server (set `NATS_URL` environment variable)

2. **Go**: Ensure you have Go 1.19+ installed.

3. **Dependencies**: Install dependencies:
   ```bash
   go mod download
   ```

## Examples

### 1. Basic Demo (`cmd/demo/main.go`)

A simple example showing the basic usage of leader election.

**What it demonstrates:**
- Basic election setup
- OnPromote and OnDemote callbacks
- Status monitoring
- Graceful shutdown

**How to run:**
```bash
# Terminal 1 - First instance
go run cmd/demo/main.go

# Terminal 2 - Second instance (to see leader election in action)
INSTANCE_ID=instance-2 go run cmd/demo/main.go
```

**Environment variables:**
- `NATS_URL`: NATS server URL (default: `nats://localhost:4222`)
- `INSTANCE_ID`: Unique instance identifier (default: `hostname-pid`)

**Expected output:**
- Instance will participate in election
- One instance becomes leader, others become followers
- Status updates every 3 seconds
- Press Ctrl+C for graceful shutdown

---

### 2. Control Manager (`examples/control_manager/main.go`)

A realistic example showing a control manager that coordinates tasks when it becomes leader.

**What it demonstrates:**
- Leader-specific task management
- Token validation before critical operations
- Context cancellation for task cleanup
- Proper resource management with WaitGroup

**How to run:**
```bash
# Terminal 1 - First instance
go run examples/control_manager/main.go

# Terminal 2 - Second instance
INSTANCE_ID=cm-2 go run examples/control_manager/main.go
```

**Environment variables:**
- `NATS_URL`: NATS server URL (default: `nats://localhost:4222`)
- `INSTANCE_ID`: Unique instance identifier (default: `hostname-pid`)

**Key features:**
- Starts leader tasks when promoted
- Validates token before each critical operation
- Stops tasks gracefully when demoted
- Handles connection failures and reconnections

**Expected output:**
- Control manager starts and waits for leadership
- When promoted, starts performing control tasks every 2 seconds
- When demoted, stops tasks gracefully
- Status updates every 5 seconds

---

### 3. Multi-Role Manager (`examples/multi_role/main.go`)

An example showing how to manage multiple role elections simultaneously.

**What it demonstrates:**
- Multiple independent role elections
- Role-specific callbacks
- Managing multiple leadership roles
- Status monitoring for all roles

**How to run:**
```bash
# Terminal 1 - First instance
go run examples/multi_role/main.go

# Terminal 2 - Second instance
INSTANCE_ID=instance-2 go run examples/multi_role/main.go
```

**Environment variables:**
- `NATS_URL`: NATS server URL (default: `nats://localhost:4222`)
- `INSTANCE_ID`: Unique instance identifier (default: `hostname-pid`)

**Key features:**
- Manages 3 roles: `database-writer`, `scheduler`, `metrics-collector`
- Each role has independent leader election
- One instance can be leader of multiple roles
- Role-specific status tracking

**Expected output:**
- Starts elections for all 3 roles
- Shows which roles this instance is leader for
- Updates status every 5 seconds
- Demonstrates that one instance can lead multiple roles

---

## Connection Handling

All examples include connection error handling and automatic reconnection:

- **Automatic Reconnection**: NATS client automatically reconnects on connection loss
- **Disconnect Handling**: Logs when connection is lost
- **Reconnect Handling**: Logs when connection is restored
- **Graceful Degradation**: Election continues to work after reconnection

The election library's connection monitoring handles:
- Grace period for brief network hiccups
- Automatic demotion if connection is lost for too long
- Leadership verification on reconnection

## Testing Scenarios

### Scenario 1: Multiple Instances Competing

Run multiple instances simultaneously to see leader election in action:

```bash
# Terminal 1
go run cmd/demo/main.go

# Terminal 2
INSTANCE_ID=instance-2 go run cmd/demo/main.go

# Terminal 3
INSTANCE_ID=instance-3 go run cmd/demo/main.go
```

Only one instance will be leader at a time.

### Scenario 2: Leader Failure

1. Start two instances
2. Wait for one to become leader
3. Kill the leader (Ctrl+C)
4. Observe the follower becoming the new leader

### Scenario 3: Network Partition

1. Start two instances
2. Wait for one to become leader
3. Stop NATS server temporarily
4. Observe leader demotion after grace period
5. Restart NATS server
6. Observe re-election

### Scenario 4: Multi-Role Leadership

1. Start two instances of `multi_role` example
2. Observe that different instances can be leaders for different roles
3. One instance might be leader for `database-writer` while another is leader for `scheduler`

## Troubleshooting

### Connection Errors

If you see connection errors:

1. **Check NATS server is running:**
   ```bash
   # Test connection
   nats pub test "hello"
   ```

2. **Check NATS_URL:**
   ```bash
   echo $NATS_URL
   # Should be something like: nats://localhost:4222
   ```

3. **Check JetStream is enabled:**
   ```bash
   # NATS server should be started with -js flag
   nats-server -js
   ```

### Bucket Creation Errors

If you see bucket creation errors:

1. **Bucket might already exist** (this is OK, examples handle this)
2. **Check JetStream is enabled** on NATS server
3. **Check permissions** if using remote NATS server

### Leader Not Elected

If no leader is elected:

1. **Check multiple instances are running** (at least one)
2. **Check all instances use the same bucket name** (default: `leaders`)
3. **Check all instances use the same group** (for the same role)
4. **Wait a few seconds** - initial election can take a moment

## Next Steps

After running the examples:

1. **Read the code** - Each example is well-commented
2. **Modify the examples** - Try changing configurations
3. **Integrate into your application** - Use patterns from examples
4. **Check the documentation** - See `docs/` directory for detailed guides

## Additional Resources

- **Main README**: See `README.md` in project root
- **Implementation Guide**: See `docs/IMPLEMENTATION_SUMMARY.md`
- **API Documentation**: Run `go doc` commands to see API details

