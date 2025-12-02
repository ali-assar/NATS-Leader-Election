package natsmock

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockConn(t *testing.T) {
	conn := NewMockConn()
	assert.Equal(t, StatusConnected, conn.Status())
}

func TestMockConn_JetStream(t *testing.T) {
	conn := NewMockConn()
	js, err := conn.JetStream()
	assert.NoError(t, err)
	assert.NotNil(t, js)
}

func TestMockConn_Disconnect(t *testing.T) {
	conn := NewMockConn()
	conn.Disconnect()
	assert.Equal(t, StatusDisconnected, conn.Status())
}

func TestMockConn_Close(t *testing.T) {
	conn := NewMockConn()
	conn.Close()
	assert.Equal(t, StatusClosed, conn.Status())
}

func TestMockConn_KeyValue(t *testing.T) {
	conn := NewMockConn()
	js, err := conn.JetStream()
	assert.NoError(t, err)
	assert.NotNil(t, js)
	kv, err := js.KeyValue("test")
	assert.NoError(t, err)
	assert.NotNil(t, kv)
}
