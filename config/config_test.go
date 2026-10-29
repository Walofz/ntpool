package config

import "testing"

func TestStratumConnectionLimitsDefaultToDisabled(t *testing.T) {
	t.Setenv("STRATUM_IDLE_TIMEOUT", "")
	t.Setenv("STRATUM_MAX_CONNS", "")

	cfg := LoadConfig()
	if cfg.StratumIdleTimeout != 0 {
		t.Fatalf("STRATUM_IDLE_TIMEOUT must default to 0 (disabled), got %d", cfg.StratumIdleTimeout)
	}
	if cfg.StratumMaxConns != 0 {
		t.Fatalf("STRATUM_MAX_CONNS must default to 0 (unlimited), got %d", cfg.StratumMaxConns)
	}
}

func TestStratumConnectionLimitsFromEnv(t *testing.T) {
	t.Setenv("STRATUM_IDLE_TIMEOUT", "600")
	t.Setenv("STRATUM_MAX_CONNS", "25")

	cfg := LoadConfig()
	if cfg.StratumIdleTimeout != 600 {
		t.Fatalf("expected STRATUM_IDLE_TIMEOUT 600, got %d", cfg.StratumIdleTimeout)
	}
	if cfg.StratumMaxConns != 25 {
		t.Fatalf("expected STRATUM_MAX_CONNS 25, got %d", cfg.StratumMaxConns)
	}
}
