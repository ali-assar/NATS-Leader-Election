package leader

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedNATSServer_FullLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	bucketName := "test-leaders"
	err = CreateKVBucket(conn, bucketName, 10*time.Second)
	require.NoError(t, err)
	defer func() {
		err := CleanupKVBucket(conn, bucketName)
		require.NoError(t, err)
	}()

	kv, err := GetKVBucket(conn, bucketName)
	require.NoError(t, err)
	assert.NotNil(t, kv)

	// Verify we can use the bucket directly
	js, err := conn.JetStream()
	require.NoError(t, err)
	kvDirect, err := js.KeyValue(bucketName)
	require.NoError(t, err)
	assert.NotNil(t, kvDirect)

	// Test KV operations
	_, err = kvDirect.Create("test-key", []byte("test-value"))
	require.NoError(t, err)

	entry, err := kvDirect.Get("test-key")
	require.NoError(t, err)
	assert.NotNil(t, entry)
	assert.Equal(t, []byte("test-value"), entry.Value())
}

func TestEmbeddedNATSServer_MultipleBuckets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	bucketNames := []string{"test-leaders1", "test-leaders2", "test-leaders3"}

	// Create all buckets
	for _, name := range bucketNames {
		err = CreateKVBucket(conn, name, 10*time.Second)
		require.NoError(t, err)
	}

	// Verify all buckets exist
	for _, name := range bucketNames {
		kv, err := GetKVBucket(conn, name)
		require.NoError(t, err, "Bucket %s should exist", name)
		assert.NotNil(t, kv, "Bucket %s should not be nil", name)
	}

	// Cleanup all buckets
	for _, name := range bucketNames {
		err = CleanupKVBucket(conn, name)
		require.NoError(t, err, "Should be able to delete bucket %s", name)
	}

	// Verify all buckets are deleted
	for _, name := range bucketNames {
		_, err = GetKVBucket(conn, name)
		require.Error(t, err, "Bucket %s should not exist after deletion", name)
	}
}

func TestEmbeddedNATSServer_ConcurrentOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := StartEmbeddedNATSServer(ctx)
	require.NoError(t, err)
	defer func() {
		err := StopEmbeddedNATSServer(server)
		require.NoError(t, err)
	}()

	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	defer conn.Close()

	numBuckets := 5
	bucketNames := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketNames[i] = fmt.Sprintf("test-leaders%d", i)
	}

	// Create buckets concurrently
	var wg sync.WaitGroup
	var createErrors sync.Map
	wg.Add(numBuckets)
	for _, name := range bucketNames {
		go func(bucketName string) {
			defer wg.Done()
			err := CreateKVBucket(conn, bucketName, 10*time.Second)
			if err != nil {
				createErrors.Store(bucketName, err)
			}
		}(name)
	}
	wg.Wait()

	// Check for creation errors
	createErrors.Range(func(key, value interface{}) bool {
		t.Errorf("Failed to create bucket %s: %v", key, value)
		return true
	})

	// Verify all buckets exist
	for _, name := range bucketNames {
		kv, err := GetKVBucket(conn, name)
		require.NoError(t, err, "Bucket %s should exist", name)
		assert.NotNil(t, kv, "Bucket %s should not be nil", name)
	}

	// Delete buckets concurrently
	var deleteErrors sync.Map
	wg.Add(numBuckets)
	for _, name := range bucketNames {
		go func(bucketName string) {
			defer wg.Done()
			err := CleanupKVBucket(conn, bucketName)
			if err != nil {
				deleteErrors.Store(bucketName, err)
			}
		}(name)
	}
	wg.Wait()

	// Check for deletion errors
	deleteErrors.Range(func(key, value interface{}) bool {
		t.Errorf("Failed to delete bucket %s: %v", key, value)
		return true
	})

	// Verify all buckets are deleted
	for _, name := range bucketNames {
		_, err = GetKVBucket(conn, name)
		require.Error(t, err, "Bucket %s should not exist after deletion", name)
	}
}
