package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the explicit, checked-in declaration of what dbstore-gen
// mirrors for one domain interface: which interface, which file, and which
// backends map to which adapter package. All three are data here, never
// inferred from file content or guessed by name shape — a prior
// name-suffix heuristic for -interface was removed because it could pick
// the wrong interface silently instead of failing loudly, which is worse
// than just requiring the human to write it down.
type Config struct {
	Interface string          `yaml:"interface"`
	Source    string          `yaml:"source"`
	Test      bool            `yaml:"test"`
	Backends  []ConfigBackend `yaml:"backends"`
}

// ConfigBackend names one backend and the adapter package that implements
// it — Adapter may be a full import path or one of builtinAdapters' short
// names (see parseBackendSpec).
type ConfigBackend struct {
	Name    string `yaml:"name"`
	Adapter string `yaml:"adapter"`
}

// loadConfig reads and validates a dbstore-gen YAML config. Source is
// resolved relative to the config file's own directory, not the current
// working directory, so the config stays correct regardless of where
// `go generate`/`dbstore-gen -config` is invoked from.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Interface == "" {
		return nil, fmt.Errorf("config %s: interface is required", path)
	}
	if cfg.Source == "" {
		return nil, fmt.Errorf("config %s: source is required", path)
	}
	if len(cfg.Backends) == 0 {
		return nil, fmt.Errorf("config %s: at least one backend is required", path)
	}
	seen := make(map[string]struct{}, len(cfg.Backends))
	for _, b := range cfg.Backends {
		if b.Name == "" {
			return nil, fmt.Errorf("config %s: a backend has an empty name", path)
		}
		if b.Adapter == "" {
			return nil, fmt.Errorf("config %s: backend %q: adapter is required", path, b.Name)
		}
		if _, exists := seen[b.Name]; exists {
			return nil, fmt.Errorf("config %s: duplicate backend name %q", path, b.Name)
		}
		seen[b.Name] = struct{}{}
	}

	if !filepath.IsAbs(cfg.Source) {
		cfg.Source = filepath.Join(filepath.Dir(path), cfg.Source)
	}
	return &cfg, nil
}
