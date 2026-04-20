package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Proxy    ProxyConfig
	Upstream UpstreamConfig
	Storage  StorageConfig
	Pricing  map[string]ModelPricing `toml:"pricing"`
}

type ProxyConfig struct {
	Listen      string   `toml:"listen"`
	IdleTimeout duration `toml:"idle_timeout"`
}

type UpstreamConfig struct {
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	Timeout duration          `toml:"timeout"`
}

type StorageConfig struct {
	DBPath        string `toml:"db_path"`
	RetentionDays int    `toml:"retention_days"`
}

type ModelPricing struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
}

// duration wraps time.Duration so it can be decoded from a TOML string like "15m".
type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

// Load reads a TOML config file from path and returns a Config with defaults applied.
func Load(path string) (*Config, error) {
	path = expandHome(path)

	cfg := &Config{}

	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("decode config %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat config %s: %w", path, err)
	}

	applyDefaults(cfg)
	cfg.Storage.DBPath = expandHome(cfg.Storage.DBPath)
	return cfg, nil
}

// DefaultPath returns the config file location. It checks the CCI_CONFIG
// environment variable first, falling back to ~/.claude/cc-insights/config.toml.
func DefaultPath() string {
	if p := os.Getenv("CCI_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(homeDir(), ".claude", "cc-insights", "config.toml")
}

// DataDir returns the default data directory.
func DataDir() string {
	return filepath.Join(homeDir(), ".claude", "cc-insights")
}

func applyDefaults(cfg *Config) {
	if cfg.Proxy.Listen == "" {
		cfg.Proxy.Listen = "127.0.0.1:4318"
	}
	if cfg.Proxy.IdleTimeout.Duration == 0 {
		cfg.Proxy.IdleTimeout.Duration = 15 * time.Minute
	}
	if cfg.Upstream.Timeout.Duration == 0 {
		cfg.Upstream.Timeout.Duration = 10 * time.Second
	}
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = "~/.claude/cc-insights/metrics.db"
	}
	if cfg.Storage.RetentionDays == 0 {
		cfg.Storage.RetentionDays = 1825
	}
	if cfg.Pricing == nil {
		cfg.Pricing = make(map[string]ModelPricing)
	}
	applyDefaultPricing(cfg.Pricing)
}

func applyDefaultPricing(pricing map[string]ModelPricing) {
	defaults := map[string]ModelPricing{
		"opus-4-6":   {Input: 5.0, Output: 25.0, CacheRead: 0.50, CacheWrite: 6.25},
		"opus-4-5":   {Input: 5.0, Output: 25.0, CacheRead: 0.50, CacheWrite: 6.25},
		"opus-4-1":   {Input: 15.0, Output: 75.0, CacheRead: 1.50, CacheWrite: 18.75},
		"sonnet-4-6": {Input: 3.0, Output: 15.0, CacheRead: 0.30, CacheWrite: 3.75},
		"sonnet-4-5": {Input: 3.0, Output: 15.0, CacheRead: 0.30, CacheWrite: 3.75},
		"sonnet-4-0": {Input: 3.0, Output: 15.0, CacheRead: 0.30, CacheWrite: 3.75},
		"haiku-4-5":  {Input: 1.0, Output: 5.0, CacheRead: 0.10, CacheWrite: 1.25},
		"haiku-3-5":  {Input: 0.80, Output: 4.0, CacheRead: 0.08, CacheWrite: 1.0},
	}
	for model, price := range defaults {
		if _, ok := pricing[model]; !ok {
			pricing[model] = price
		}
	}
}

func expandHome(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		return filepath.Join(homeDir(), path[1:])
	}
	return path
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return home
}
