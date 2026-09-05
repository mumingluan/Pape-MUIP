package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Peer struct {
	BaseURL        string `yaml:"base_url" json:"base_url"`
	AuthToken      string `yaml:"auth_token" json:"-"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"-"`
}

type Config struct {
	Listen        string          `yaml:"listen"`
	AdminUser     string          `yaml:"admin_user"`
	AdminPassword string          `yaml:"admin_password"`
	LanguageDB    string          `yaml:"language_db"`
	SDK           Peer            `yaml:"sdk_inner"`
	BOOI          map[string]Peer `yaml:"booi_inner"`
	BaseDir       string          `yaml:"-"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.BaseDir = filepath.Dir(abs)
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:18080"
	}
	if cfg.AdminUser == "" || cfg.AdminPassword == "" {
		return nil, errors.New("admin_user and admin_password are required")
	}
	if cfg.LanguageDB == "" {
		cfg.LanguageDB = "languages.sqlite"
	}
	if err := validatePeer("sdk_inner", cfg.SDK); err != nil {
		return nil, err
	}
	if len(cfg.BOOI) == 0 {
		return nil, errors.New("booi_inner requires at least one server")
	}
	for name, peer := range cfg.BOOI {
		if err := validatePeer("booi_inner."+name, peer); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func (c *Config) Resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.BaseDir, path)
}

func validatePeer(name string, peer Peer) error {
	parsed, err := url.Parse(peer.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%s.base_url must be an absolute HTTP(S) URL", name)
	}
	if strings.TrimSpace(peer.AuthToken) == "" {
		return fmt.Errorf("%s.auth_token is required", name)
	}
	return nil
}
