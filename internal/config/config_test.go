package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInitAndLoad(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("OPENCOMFY_DATA", filepath.Join(dir, "data"))
	key, err := InitFiles(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Fatal("expected generated key")
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8788" {
		t.Fatal(cfg.Listen)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestAuthDisabledNonLoopback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("listen: \":8788\"\nauth:\n  disabled: true\n"), 0o644)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected refuse")
	}
}
