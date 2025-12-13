package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ali-assar/NATS-Leader-Election/leader"
	"github.com/nats-io/nats.go"
)

func main() {
	// Get NATS URL from environment or use default
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

	fmt.Printf("Starting leader election demo (Instance ID: %s)\n", instanceID)
	fmt.Printf("Connecting to NATS at: %s\n", natsURL)

	// Step 1: Connect to NATS with reconnection handling
	nc, err := nats.Connect(natsURL,
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				log.Printf("⚠️  NATS disconnected: %v", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("✓ NATS reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("⚠️  NATS connection closed")
		}),
	)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()
	fmt.Println("✓ Connected to NATS")

	// Step 2: Get JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to get JetStream: %v", err)
	}

	// Step 3: Create or get KV bucket
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

	// Step 4: Create election configuration
	cfg := leader.ElectionConfig{
		Bucket:             bucketName,
		Group:              "demo-group",
		InstanceID:         instanceID,
		TTL:                10 * time.Second,
		HeartbeatInterval:  1 * time.Second,
		ValidationInterval: 5 * time.Second,
	}

	// Step 5: Create election instance
	election, err := leader.NewElectionWithConn(nc, cfg)
	if err != nil {
		log.Fatalf("Failed to create election: %v", err)
	}
	fmt.Println("✓ Election instance created")

	// Step 6: Register OnPromote callback
	// This is called when THIS instance becomes leader
	election.OnPromote(func(ctx context.Context, token string) {
		fmt.Printf("PROMOTED to leader! Token: %s\n", token)
		fmt.Println("   → This instance is now the leader")
		fmt.Println("   → You can start leader-specific tasks here")
	})

	// Step 7: Register OnDemote callback
	// This is called when THIS instance loses leadership
	election.OnDemote(func() {
		fmt.Println("DEMOTED from leader")
		fmt.Println("   → This instance is no longer the leader")
		fmt.Println("   → Stop leader-specific tasks here")
	})

	// Step 8: Start the election
	ctx := context.Background()
	if err := election.Start(ctx); err != nil {
		log.Fatalf("Failed to start election: %v", err)
	}
	fmt.Println("✓ Election started, participating in leader election...")

	// Step 9: Print status periodically
	statusTicker := time.NewTicker(3 * time.Second)
	defer statusTicker.Stop()

	go func() {
		for {
			select {
			case <-statusTicker.C:
				status := election.Status()
				if status.IsLeader {
					fmt.Printf("Status: LEADER | ID: %s | Token: %s | State: %s\n",
						status.LeaderID, status.Token, status.State)
				} else {
					fmt.Printf("Status: FOLLOWER | Current Leader: %s | State: %s\n",
						status.LeaderID, status.State)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Step 10: Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	fmt.Println("\n✓ Running... Press Ctrl+C to stop gracefully")
	fmt.Println("  (Try running multiple instances to see leader election in action)")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\n⏹️  Shutdown signal received, stopping gracefully...")

	// Step 11: Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = election.StopWithContext(shutdownCtx, leader.StopOptions{
		DeleteKey:     true, // Delete key for fast failover
		WaitForDemote: true, // Wait for OnDemote callback to complete
	})
	if err != nil {
		log.Printf("Error during shutdown: %v", err)
	} else {
		fmt.Println("✓ Shutdown complete")
	}
}
