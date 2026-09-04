package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnprotectedInnerPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `admin_user: admin
admin_password: secret
sdk_inner:
  base_url: http://127.0.0.1:18081
  auth_token: ""
booi_inner:
  "500058":
    base_url: http://127.0.0.1:18082
    auth_token: protected
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "sdk_inner.auth_token") {
		t.Fatalf("unexpected validation result: %v", err)
	}
}

func TestLoadDefaultsAndResolvesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := `admin_user: admin
admin_password: secret
sdk_inner:
  base_url: http://127.0.0.1:18081
  auth_token: sdk-secret
booi_inner:
  "500058":
    base_url: http://127.0.0.1:18082
    auth_token: booi-secret
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:18080" || cfg.LanguageDB != "languages.sqlite" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if got, want := cfg.Resolve(cfg.LanguageDB), filepath.Join(dir, "languages.sqlite"); got != want {
		t.Fatalf("Resolve()=%q, want %q", got, want)
	}
}
