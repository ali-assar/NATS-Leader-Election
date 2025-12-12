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

// RoleManager manages multiple role elections.
// Each role has its own election, and this instance can be leader of different roles.
type RoleManager struct {
	elections map[string]leader.Election
	mu        sync.RWMutex
}

// NewRoleManager creates a new RoleManager instance.
func NewRoleManager() *RoleManager {
	return &RoleManager{
		elections: make(map[string]leader.Election),
	}
}

// AddRole adds a new role election to the manager.
func (rm *RoleManager) AddRole(roleName string, election leader.Election) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.elections[roleName] = election
}

// GetRoleStatus returns the status of all roles.
func (rm *RoleManager) GetRoleStatus() map[string]bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	status := make(map[string]bool)
	for role, election := range rm.elections {
		status[role] = election.IsLeader()
	}
	return status
}

// PrintStatus prints the current status of all roles.
func (rm *RoleManager) PrintStatus() {
	status := rm.GetRoleStatus()
	fmt.Println("\n Role Status:")
	for role, isLeader := range status {
		if isLeader {
			fmt.Printf("  ✓ %s: LEADER\n", role)
		} else {
			// Get current leader ID for this role
			rm.mu.RLock()
			election := rm.elections[role]
			rm.mu.RUnlock()
			leaderID := election.LeaderID()
			if leaderID != "" {
				fmt.Printf("  ○ %s: FOLLOWER (Leader: %s)\n", role, leaderID)
			} else {
				fmt.Printf("  ○ %s: FOLLOWER (No leader)\n", role)
			}
		}
	}
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

	fmt.Printf("Starting multi-role leader election (Instance ID: %s)\n", instanceID)
	fmt.Printf("Connecting to NATS at: %s\n", natsURL)

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()
	fmt.Println("✓ Connected to NATS")

	// Get JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to get JetStream: %v", err)
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
			log.Fatalf("Failed to create or get KV bucket: %v", err)
		}
	}
	fmt.Printf("✓ KV bucket '%s' ready\n", bucketName)

	// Define roles
	roles := []string{
		"database-writer",
		"scheduler",
		"metrics-collector",
	}

	// Create RoleManager
	roleManager := NewRoleManager()

	// Create election for each role
	for _, role := range roles {
		cfg := leader.ElectionConfig{
			Bucket:             bucketName,
			Group:              role, // Different group = different role
			InstanceID:         instanceID,
			TTL:                10 * time.Second,
			HeartbeatInterval:  1 * time.Second,
			ValidationInterval: 5 * time.Second,
		}

		election, err := leader.NewElectionWithConn(nc, cfg)
		if err != nil {
			log.Fatalf("Failed to create election for role '%s': %v", role, err)
		}

		// Register role-specific callbacks
		roleName := role // Capture for closure
		election.OnPromote(func(ctx context.Context, token string) {
			fmt.Printf(" PROMOTED to leader of role '%s' (token: %s)\n", roleName, token)
			fmt.Printf("   → Starting %s tasks...\n", roleName)

			// In a real application, you would start role-specific tasks here
			// For example:
			// - database-writer: Start database write operations
			// - scheduler: Start task scheduling
			// - metrics-collector: Start metrics aggregation
		})

		election.OnDemote(func() {
			fmt.Printf(" DEMOTED from leader of role '%s'\n", roleName)
			fmt.Printf("   → Stopping %s tasks...\n", roleName)
		})

		// Add to role manager
		roleManager.AddRole(role, election)

		// Start the election
		ctx := context.Background()
		if err := election.Start(ctx); err != nil {
			log.Fatalf("Failed to start election for role '%s': %v", role, err)
		}

		fmt.Printf("✓ Election started for role '%s'\n", role)
	}

	fmt.Println("\n✓ All role elections started")
	fmt.Println("  (Each role has its own independent leader election)")
	fmt.Println("  (This instance can be leader of multiple roles simultaneously)")

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Print status periodically
	statusTicker := time.NewTicker(5 * time.Second)
	defer statusTicker.Stop()

	ctx := context.Background()
	go func() {
		for {
			select {
			case <-statusTicker.C:
				roleManager.PrintStatus()
			case <-ctx.Done():
				return
			}
		}
	}()

	fmt.Println("✓ Running... Press Ctrl+C to stop gracefully")
	fmt.Println("  (Try running multiple instances to see multi-role election in action)")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\n⏹️  Shutdown signal received, stopping gracefully...")

	// Graceful shutdown for all elections
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	roleManager.mu.RLock()
	elections := make([]leader.Election, 0, len(roleManager.elections))
	for _, election := range roleManager.elections {
		elections = append(elections, election)
	}
	roleManager.mu.RUnlock()

	// Stop all elections
	for role, election := range roleManager.elections {
		fmt.Printf("Stopping election for role '%s'...\n", role)
		err := election.StopWithContext(shutdownCtx, leader.StopOptions{
			DeleteKey:     true,
			WaitForDemote: true,
		})
		if err != nil {
			log.Printf("Error stopping election for role '%s': %v", role, err)
		}
	}

	fmt.Println("✓ Shutdown complete")
}
