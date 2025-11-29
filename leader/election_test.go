package leader

import (
	"testing"
	"time"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ElectionConfig
		wantErr bool
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
			}
			if err != nil && !tt.wantErr {
				t.Errorf("validateConfig() unexpected error: %v", err)
			}
		})
	}
}
