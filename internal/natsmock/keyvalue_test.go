package natsmock

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockKeyValue_New(t *testing.T) {
	kv := NewMockKeyValue()
	assert.NotNil(t, kv)
}

func TestCreate_Success(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)
}

func TestCreate_KeyAlreadyExists(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)

	rev, err = kv.Create("test", []byte("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key already exists")
	assert.Equal(t, uint64(0), rev)
}

func TestCreate_DifferentKeys(t *testing.T) {
	kv := NewMockKeyValue()

	rev1, err := kv.Create("key1", []byte("value1"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev1)

	rev2, err := kv.Create("key2", []byte("value2"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), rev2)
}

func TestUpdate_Success(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)

	rev, err = kv.Update("test", []byte("test2"), 1)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), rev)
}

func TestUpdate_KeyNotFound(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Update("test", []byte("test2"), 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
	assert.Equal(t, uint64(0), rev)
}

func TestUpdate_RevisionMismatch(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)

	rev, err = kv.Update("test", []byte("test2"), 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "revision mismatch")
	assert.Equal(t, uint64(0), rev)
}

func TestUpdate_DifferentKeys(t *testing.T) {
	kv := NewMockKeyValue()

	rev1, err := kv.Create("key1", []byte("value1"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev1)

	rev2, err := kv.Create("key2", []byte("value2"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), rev2)
}

func TestGet_Success(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)

	entry, err := kv.Get("test")
	assert.NoError(t, err)
	assert.Equal(t, "test", entry.Key())
	assert.Equal(t, []byte("test"), entry.Value())
	assert.Equal(t, uint64(1), entry.Revision())
}

func TestGet_KeyNotFound(t *testing.T) {
	kv := NewMockKeyValue()

	entry, err := kv.Get("test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
	assert.Nil(t, entry)
}

func TestGet_DifferentKeys(t *testing.T) {
	kv := NewMockKeyValue()

	rev1, err := kv.Create("key1", []byte("value1"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev1)

	rev2, err := kv.Create("key2", []byte("value2"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), rev2)
}

func TestDelete_Success(t *testing.T) {
	kv := NewMockKeyValue()

	rev, err := kv.Create("test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev)

	err = kv.Delete("test")
	assert.NoError(t, err)
}

func TestDelete_KeyNotFound(t *testing.T) {
	kv := NewMockKeyValue()

	err := kv.Delete("test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestDelete_DifferentKeys(t *testing.T) {
	kv := NewMockKeyValue()

	rev1, err := kv.Create("key1", []byte("value1"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), rev1)

	rev2, err := kv.Create("key2", []byte("value2"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), rev2)

	err = kv.Delete("key1")
	assert.NoError(t, err)
}
