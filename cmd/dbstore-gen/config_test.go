package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "user_repo.gen.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_Valid(t *testing.T) {
	path := writeConfig(t, `
interface: UserRepository
source: user_repo.go
test: true
backends:
  - name: sqlite
    adapter: github.com/loykin/dbstore/adapters/sqlx
  - name: rest
    adapter: github.com/loykin/dbstore/adapters/rest
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interface != "UserRepository" {
		t.Fatalf("Interface = %q", cfg.Interface)
	}
	// source is resolved relative to the config file's own directory.
	wantSource := filepath.Join(filepath.Dir(path), "user_repo.go")
	if cfg.Source != wantSource {
		t.Fatalf("Source = %q, want %q", cfg.Source, wantSource)
	}
	if !cfg.Test {
		t.Fatal("Test = false, want true")
	}
	if len(cfg.Backends) != 2 || cfg.Backends[0].Name != "sqlite" || cfg.Backends[1].Name != "rest" {
		t.Fatalf("Backends = %+v", cfg.Backends)
	}
}

func TestLoadConfig_MissingInterface(t *testing.T) {
	path := writeConfig(t, `
source: user_repo.go
backends:
  - name: sqlite
    adapter: sqlite
`)
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "interface is required") {
		t.Fatalf("err = %v, want interface-required error", err)
	}
}

func TestLoadConfig_MissingSource(t *testing.T) {
	path := writeConfig(t, `
interface: UserRepository
backends:
  - name: sqlite
    adapter: sqlite
`)
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("err = %v, want source-required error", err)
	}
}

func TestLoadConfig_NoBackends(t *testing.T) {
	path := writeConfig(t, `
interface: UserRepository
source: user_repo.go
`)
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "at least one backend") {
		t.Fatalf("err = %v, want backend-required error", err)
	}
}

func TestLoadConfig_DuplicateBackendName(t *testing.T) {
	path := writeConfig(t, `
interface: UserRepository
source: user_repo.go
backends:
  - name: sqlite
    adapter: sqlite
  - name: sqlite
    adapter: postgres
`)
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate backend") {
		t.Fatalf("err = %v, want duplicate-backend error", err)
	}
}

func TestLoadConfig_BackendMissingAdapter(t *testing.T) {
	path := writeConfig(t, `
interface: UserRepository
source: user_repo.go
backends:
  - name: sqlite
`)
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "adapter is required") {
		t.Fatalf("err = %v, want adapter-required error", err)
	}
}
