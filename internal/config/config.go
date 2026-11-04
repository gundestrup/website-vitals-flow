// Package config loads wvf.yml plus WVF_* environment overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved runtime configuration.
type Config struct {
	Listen      string        `yaml:"listen"`       // serve bind address
	StateDB     string        `yaml:"state_db"`     // sqlite file path
	BrowserPath string        `yaml:"browser_path"` // explicit chrome/headless-shell binary
	Samples     int           `yaml:"samples"`      // loads per scan
	ScanTimeout time.Duration `yaml:"scan_timeout"` // whole-scan deadline
	KeepLast    int           `yaml:"keep_last"`    // runs kept per site
	MaxAgeDays  int           `yaml:"max_age_days"` // runs older than this are pruned
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Listen:      "127.0.0.1:8420",
		StateDB:     "state.db",
		Samples:     3,
		ScanTimeout: 90 * time.Second,
		KeepLast:    100,
		MaxAgeDays:  56,
	}
}

// Load reads path (missing file = defaults) and applies WVF_* env overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			if !os.IsNotExist(err) {
				return cfg, fmt.Errorf("read config %s: %w", path, err)
			}
		} else if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	applyEnv(&cfg)
	return cfg, cfg.validate()
}

func applyEnv(c *Config) {
	if v := os.Getenv("WVF_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("WVF_STATE_DB"); v != "" {
		c.StateDB = v
	}
	if v := os.Getenv("WVF_BROWSER_PATH"); v != "" {
		c.BrowserPath = v
	}
	if v := os.Getenv("WVF_SAMPLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Samples = n
		}
	}
}

func (c Config) validate() error {
	if c.Samples < 1 {
		return fmt.Errorf("samples must be >= 1, got %d", c.Samples)
	}
	if c.ScanTimeout < 5*time.Second {
		return fmt.Errorf("scan_timeout too small: %s", c.ScanTimeout)
	}
	if c.KeepLast < 1 {
		return fmt.Errorf("keep_last must be >= 1, got %d", c.KeepLast)
	}
	return nil
}
