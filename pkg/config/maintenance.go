package config

import (
	"fmt"
	"strconv"
)

// MaintenanceConfig is environment-only; retention is not editable over HTTP.
type MaintenanceConfig struct {
	LogRetentionDays int
}

// LoadMaintenanceFromEnv resolves the retention policy before starting maintenance.
func LoadMaintenanceFromEnv() (MaintenanceConfig, error) {
	cfg := MaintenanceConfig{LogRetentionDays: 30}
	if raw, name, ok := LookupEnvForSetting("log_retention_days"); ok {
		days, err := strconv.Atoi(raw)
		// Bound the value to SQLite's supported date range and reject accidental
		// zero/negative values rather than immediately deleting recent history.
		if err != nil || days < 1 || days > 365000 {
			return cfg, fmt.Errorf("%s must be an integer between 1 and 365000", name)
		}
		cfg.LogRetentionDays = days
	}
	return cfg, nil
}
