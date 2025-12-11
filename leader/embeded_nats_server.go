package leader

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// StartEmbeddedNATSServer starts an embedded NATS server with JetStream enabled.
// The server uses a temporary directory for JetStream storage, which is cleaned up
// when the server is shut down.
//
// The server will be automatically shut down when the context is cancelled.
// It's recommended to call StopEmbeddedNATSServer or server.Shutdown() for clean shutdown.
func StartEmbeddedNATSServer(ctx context.Context) (*server.Server, error) {
	// Create a unique temporary directory for JetStream storage
	// This prevents conflicts when running multiple tests in parallel
	storeDir, err := os.MkdirTemp("", "nats-js-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	opts := &server.Options{
		ServerName: "nats-test",
		JetStream:  true,
		StoreDir:   storeDir,
		Port:       -1, // Random port
		Host:       "127.0.0.1",
		// Disable logging for cleaner test output
		NoLog:  true,
		NoSigs: true,
	}

	s, err := server.NewServer(opts)
	if err != nil {
		os.RemoveAll(storeDir) // Cleanup on error
		return nil, fmt.Errorf("failed to create NATS server: %w", err)
	}

	// Start server in a goroutine
	go s.Start()

	// Wait for server to be ready
	if !s.ReadyForConnections(5 * time.Second) {
		os.RemoveAll(storeDir) // Cleanup on error
		return nil, fmt.Errorf("server not ready within timeout")
	}

	// Monitor context cancellation and shut down server if context is cancelled
	go func() {
		<-ctx.Done()
		if s.Running() {
			s.Shutdown()
		}
		// Cleanup store directory after shutdown
		os.RemoveAll(storeDir)
	}()

	return s, nil
}

// StopEmbeddedNATSServer properly shuts down the embedded NATS server and cleans up resources.
// This function should be called when you're done with the server to ensure clean shutdown.
//
// After calling this function, the server cannot be used anymore.
func StopEmbeddedNATSServer(s *server.Server) error {
	if s == nil {
		return nil
	}

	// Get the store directory before shutdown
	storeDir := s.StoreDir()

	// Shutdown the server gracefully
	s.Shutdown()

	// Wait for server to fully stop (with timeout)
	deadline := time.Now().Add(5 * time.Second)
	for s.Running() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	// Cleanup the JetStream store directory
	if storeDir != "" {
		if err := os.RemoveAll(storeDir); err != nil {
			return fmt.Errorf("failed to cleanup store directory %s: %w", storeDir, err)
		}
	}

	return nil
}

// CreateKVBucket creates a KV bucket for testing.
// If the bucket already exists, it returns nil (no error).
// This is useful for integration tests where you need a KV bucket.
func CreateKVBucket(conn *nats.Conn, bucketName string, ttl time.Duration) error {
	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	js, err := conn.JetStream()
	if err != nil {
		return fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Check if bucket already exists
	_, err = js.KeyValue(bucketName)
	if err == nil {
		return nil // Bucket already exists, no error
	}

	// Create bucket with TTL
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		TTL:     ttl,
		Storage: nats.FileStorage,
	})

	if err != nil {
		return fmt.Errorf("failed to create KV bucket %s: %w", bucketName, err)
	}

	return nil
}

// CleanupKVBucket deletes a KV bucket.
// If the bucket doesn't exist, it returns nil (no error).
// This is useful for cleaning up after integration tests.
func CleanupKVBucket(conn *nats.Conn, bucketName string) error {
	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	js, err := conn.JetStream()
	if err != nil {
		return fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Check if bucket exists
	_, err = js.KeyValue(bucketName)
	if err != nil {
		// Bucket doesn't exist, that's fine
		return nil
	}

	// Delete the bucket using JetStream context
	err = js.DeleteKeyValue(bucketName)
	if err != nil {
		return fmt.Errorf("failed to delete KV bucket %s: %w", bucketName, err)
	}

	return nil
}

// GetKVBucket gets an existing KV bucket.
// Returns an error if the bucket doesn't exist.
func GetKVBucket(conn *nats.Conn, bucketName string) (nats.KeyValue, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	js, err := conn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	kv, err := js.KeyValue(bucketName)
	if err != nil {
		return nil, fmt.Errorf("bucket %s does not exist: %w", bucketName, err)
	}

	return kv, nil
}

// EnsureKVBucket ensures a KV bucket exists, creating it if necessary.
// This is a convenience function that combines CreateKVBucket and GetKVBucket.
func EnsureKVBucket(conn *nats.Conn, bucketName string, ttl time.Duration) (nats.KeyValue, error) {
	if err := CreateKVBucket(conn, bucketName, ttl); err != nil {
		return nil, err
	}

	return GetKVBucket(conn, bucketName)
}
