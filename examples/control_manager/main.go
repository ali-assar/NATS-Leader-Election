package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/leader"
	"github.com/nats-io/nats.go"
)

// ControlManager manages leader-specific tasks for the control manager role.
// It handles starting and stopping tasks when leadership is gained or lost.
type ControlManager struct {
	election leader.Election
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.RWMutex
}

func NewControlManager(election leader.Election) *ControlManager {
	return &ControlManager{
		election: election,
	}
}

// StartLeaderTasks starts the leader-specific tasks.
// This should be called from the OnPromote callback.
// The context will be cancelled when leadership is lost or election stops.
func (c *ControlManager) StartLeaderTasks(ctx context.Context, token string) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		fmt.Printf("[ControlManager] Starting leader tasks (token: %s)\n", token)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				fmt.Println("[ControlManager] Context cancelled, stopping leader tasks")
				return
			case <-ticker.C:
				// Validate token before performing critical operations
				if !c.election.ValidateTokenOrDemote(ctx) {
					fmt.Println("[ControlManager] Token invalid, stopping tasks")
					return
				}

				fmt.Println("[ControlManager] Performing control task...")
				// In a real application, you would do actual work here:
				// - Write to database
				// - Schedule tasks
				// - Coordinate with other services
			}
		}
	}()
}

// StopLeaderTasks stops all leader-specific tasks and waits for them to complete.
// This should be called from the OnDemote callback.
func (c *ControlManager) StopLeaderTasks() {
	fmt.Println("[ControlManager] Stopping leader tasks...")

	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
		c.ctx = nil
	}
	c.mu.Unlock()

	// Wait for all tasks to complete
	c.wg.Wait()
	fmt.Println("[ControlManager] Leader tasks stopped")
}

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	// Get instance ID from environment or use default
	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		hostname, _ := os.Hostname()
		instanceID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	fmt.Printf("[ControlManager] Starting with instance ID: %s\n", instanceID)
	fmt.Printf("[ControlManager] Connecting to NATS at: %s\n", natsURL)

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("[ControlManager] Failed to connect to NATS: %v", err)
	}
	defer nc.Close()
	fmt.Println("[ControlManager] Connected to NATS")

	// Get JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("[ControlManager] Failed to get JetStream: %v", err)
	}

	// Create or get KV bucket
	bucketName := "leaders"
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		TTL:     10 * time.Second,
		Storage: nats.FileStorage,
	})
	if err != nil {
		// Check if bucket already exists (that's OK, we can use it)
		_, getErr := js.KeyValue(bucketName)
		if getErr != nil {
			log.Fatalf("[ControlManager] Failed to create or get KV bucket: %v", err)
		}
	}
	fmt.Printf("[ControlManager] KV bucket '%s' ready\n", bucketName)

	// Create election configuration
	cfg := leader.ElectionConfig{
		Bucket:             bucketName,
		Group:              "control-manager",
		InstanceID:         instanceID,
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 5 * time.Second,
	}

	// Create election instance
	election, err := leader.NewElectionWithConn(nc, cfg)
	if err != nil {
		log.Fatalf("[ControlManager] Failed to create election: %v", err)
	}

	// Create control manager
	cm := NewControlManager(election)

	// Register OnPromote callback
	election.OnPromote(func(ctx context.Context, token string) {
		fmt.Printf("[ControlManager] PROMOTED to leader (token: %s)\n", token)

		// Create context for leader tasks
		// This context will be cancelled when we're demoted
		taskCtx, cancel := context.WithCancel(ctx)

		// Store context and cancel function
		cm.mu.Lock()
		cm.ctx = taskCtx
		cm.cancel = cancel
		cm.mu.Unlock()

		// Start leader tasks
		cm.StartLeaderTasks(taskCtx, token)
	})

	// Register OnDemote callback
	election.OnDemote(func() {
		fmt.Println("[ControlManager] DEMOTED from leader")
		cm.StopLeaderTasks()
	})

	// Start the election
	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		log.Fatalf("[ControlManager] Failed to start election: %v", err)
	}
	fmt.Println("[ControlManager] Election started, waiting for leadership...")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Print status periodically
	statusTicker := time.NewTicker(5 * time.Second)
	defer statusTicker.Stop()

	go func() {
		for {
			select {
			case <-statusTicker.C:
				status := election.Status()
				if status.IsLeader {
					fmt.Printf("[ControlManager] Status: LEADER (ID: %s, Token: %s)\n",
						status.LeaderID, status.Token)
				} else {
					fmt.Printf("[ControlManager] Status: FOLLOWER (Leader: %s)\n",
						status.LeaderID)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\n[ControlManager] Shutdown signal received, stopping gracefully...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = election.StopWithContext(shutdownCtx, leader.StopOptions{
		DeleteKey:     true, // Delete key for fast failover
		WaitForDemote: true, // Wait for OnDemote callback to complete
	})
	if err != nil {
		log.Printf("[ControlManager] Error during shutdown: %v", err)
	} else {
		fmt.Println("[ControlManager] Shutdown complete")
	}
}
