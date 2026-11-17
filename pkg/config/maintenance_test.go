package config

import "testing"

func TestMaintenanceRetention(t *testing.T) {
	t.Setenv("CALLIMACHUS_LOG_RETENTION_DAYS", "")
	t.Setenv("LOG_RETENTION_DAYS", "7") // unprefixed aliases are unsupported
	cfg, err := LoadMaintenanceFromEnv()
	if err != nil || cfg.LogRetentionDays != 30 {
		t.Fatalf("default: %+v, %v", cfg, err)
	}
	for _, raw := range []string{"7", "1", "365000", "0", "-1", "oops", "365001", "99999999999999999999"} {
		t.Setenv("CALLIMACHUS_LOG_RETENTION_DAYS", raw)
		cfg, err := LoadMaintenanceFromEnv()
		if raw == "7" || raw == "1" || raw == "365000" {
			if err != nil || cfg.LogRetentionDays < 1 {
				t.Fatalf("%s: %+v, %v", raw, cfg, err)
			}
		} else if err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
