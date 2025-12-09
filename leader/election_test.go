package leader

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ElectionConfig
		wantErr     bool
		wantErrType string // "ValidationError" or ""
		wantField   string // Expected field name in error
	}{
		{
			name: "valid config",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "empty bucket",
			cfg: ElectionConfig{
				Bucket:            "",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "empty group",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "empty instance ID",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "zero TTL",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               0,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "negative TTL",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               -1 * time.Second,
				HeartbeatInterval: 2 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "zero heartbeat interval",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 0,
			},
			wantErr: true,
		},
		{
			name: "negative heartbeat interval",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: -1 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "TTL too short (less than 3x heartbeat)",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               5 * time.Second,
				HeartbeatInterval: 2 * time.Second, // TTL should be >= 6 seconds
			},
			wantErr: true,
		},
		{
			name: "TTL exactly 3x heartbeat (valid)",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               6 * time.Second,
				HeartbeatInterval: 2 * time.Second, // TTL = 3x heartbeat, valid
			},
			wantErr: false,
		},
		{
			name: "TTL greater than 3x heartbeat (valid)",
			cfg: ElectionConfig{
				Bucket:            "leaders",
				Group:             "test-group",
				InstanceID:        "instance-1",
				TTL:               10 * time.Second,
				HeartbeatInterval: 2 * time.Second, // TTL = 5x heartbeat, valid
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				var validationErr *ValidationError
				if errors.As(err, &validationErr) {
					if tt.wantField != "" {
						assert.Equal(t, tt.wantField, validationErr.Field,
							"error should mention the correct field")
						assert.Contains(t, err.Error(), tt.wantField,
							"error message should contain field name")
					}
				} else {
					t.Errorf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestValidateConfig_ValidationInterval(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ElectionConfig
		wantErr bool
	}{
		{
			name: "ValidationInterval >= HeartbeatInterval (valid)",
			cfg: ElectionConfig{
				Bucket:             "leaders",
				Group:              "test-group",
				InstanceID:         "instance-1",
				TTL:                10 * time.Second,
				HeartbeatInterval:  2 * time.Second,
				ValidationInterval: 2 * time.Second, // Equal to HeartbeatInterval
			},
			wantErr: false,
		},
		{
			name: "ValidationInterval > HeartbeatInterval (valid)",
			cfg: ElectionConfig{
				Bucket:             "leaders",
				Group:              "test-group",
				InstanceID:         "instance-1",
				TTL:                10 * time.Second,
				HeartbeatInterval:  2 * time.Second,
				ValidationInterval: 5 * time.Second, // Greater than HeartbeatInterval
			},
			wantErr: false,
		},
		{
			name: "ValidationInterval < HeartbeatInterval (invalid)",
			cfg: ElectionConfig{
				Bucket:             "leaders",
				Group:              "test-group",
				InstanceID:         "instance-1",
				TTL:                10 * time.Second,
				HeartbeatInterval:  2 * time.Second,
				ValidationInterval: 1 * time.Second, // Less than HeartbeatInterval
			},
			wantErr: true,
		},
		{
			name: "ValidationInterval = 0 (valid, disabled)",
			cfg: ElectionConfig{
				Bucket:             "leaders",
				Group:              "test-group",
				InstanceID:         "instance-1",
				TTL:                10 * time.Second,
				HeartbeatInterval:  2 * time.Second,
				ValidationInterval: 0, // Disabled
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				var validationErr *ValidationError
				assert.ErrorAs(t, err, &validationErr)
				assert.Equal(t, "ValidationInterval", validationErr.Field)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateConfig_DisconnectGracePeriod(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ElectionConfig
		wantErr bool
	}{
		{
			name: "DisconnectGracePeriod >= 2x HeartbeatInterval (valid)",
			cfg: ElectionConfig{
				Bucket:                "leaders",
				Group:                 "test-group",
				InstanceID:            "instance-1",
				TTL:                   10 * time.Second,
				HeartbeatInterval:     2 * time.Second,
				DisconnectGracePeriod: 4 * time.Second, // Exactly 2x
			},
			wantErr: false,
		},
		{
			name: "DisconnectGracePeriod > 2x HeartbeatInterval (valid)",
			cfg: ElectionConfig{
				Bucket:                "leaders",
				Group:                 "test-group",
				InstanceID:            "instance-1",
				TTL:                   10 * time.Second,
				HeartbeatInterval:     2 * time.Second,
				DisconnectGracePeriod: 6 * time.Second, // Greater than 2x
			},
			wantErr: false,
		},
		{
			name: "DisconnectGracePeriod < 2x HeartbeatInterval (invalid)",
			cfg: ElectionConfig{
				Bucket:                "leaders",
				Group:                 "test-group",
				InstanceID:            "instance-1",
				TTL:                   10 * time.Second,
				HeartbeatInterval:     2 * time.Second,
				DisconnectGracePeriod: 1 * time.Second, // Less than 2x
			},
			wantErr: true,
		},
		{
			name: "DisconnectGracePeriod = 0 (valid, uses default)",
			cfg: ElectionConfig{
				Bucket:                "leaders",
				Group:                 "test-group",
				InstanceID:            "instance-1",
				TTL:                   10 * time.Second,
				HeartbeatInterval:     2 * time.Second,
				DisconnectGracePeriod: 0, // Uses default
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				var validationErr *ValidationError
				assert.ErrorAs(t, err, &validationErr)
				assert.Equal(t, "DisconnectGracePeriod", validationErr.Field)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
