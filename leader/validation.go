package leader

import "fmt"

func validateConfig(cfg ElectionConfig) error {
	if cfg.Bucket == "" {
		return NewValidationError("Bucket", cfg.Bucket, "bucket name is required")
	}
	if cfg.Group == "" {
		return NewValidationError("Group", cfg.Group, "group name is required")
	}
	if cfg.InstanceID == "" {
		return NewValidationError("InstanceID", cfg.InstanceID, "instance ID is required")
	}

	if cfg.TTL <= 0 {
		return NewValidationError("TTL", cfg.TTL, "TTL must be positive")
	}

	if cfg.HeartbeatInterval <= 0 {
		return NewValidationError("HeartbeatInterval", cfg.HeartbeatInterval,
			"heartbeat interval must be positive")
	}

	minTTL := cfg.HeartbeatInterval * 3
	if cfg.TTL < minTTL {
		return NewValidationError("TTL", cfg.TTL,
			fmt.Sprintf("TTL (%v) must be at least 3x HeartbeatInterval (%v), minimum %v",
				cfg.TTL, cfg.HeartbeatInterval, minTTL))
	}

	// Check ValidationInterval (if set)
	if cfg.ValidationInterval > 0 {
		if cfg.ValidationInterval < cfg.HeartbeatInterval {
			return NewValidationError("ValidationInterval", cfg.ValidationInterval,
				fmt.Sprintf("validation interval (%v) should be >= HeartbeatInterval (%v)",
					cfg.ValidationInterval, cfg.HeartbeatInterval))
		}
	}

	if cfg.DisconnectGracePeriod > 0 {
		minGracePeriod := cfg.HeartbeatInterval * 2
		if cfg.DisconnectGracePeriod < minGracePeriod {
			return NewValidationError("DisconnectGracePeriod", cfg.DisconnectGracePeriod,
				fmt.Sprintf("disconnect grace period (%v) should be >= 2x HeartbeatInterval (%v), minimum %v",
					cfg.DisconnectGracePeriod, cfg.HeartbeatInterval, minGracePeriod))
		}
	}

	if cfg.MaxConsecutiveFailures < 0 {
		return NewValidationError("MaxConsecutiveFailures", cfg.MaxConsecutiveFailures, "must be >= 0")
	}

	return nil
}
