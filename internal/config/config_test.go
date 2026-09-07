package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenConfigDoesNotExist(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.State.DBPath == "" {
		t.Fatal("expected default database path")
	}
}
