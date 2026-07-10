package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestAdapter_Configure(t *testing.T) {
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{
		"sources": {
			"primary": {
				"driver": "sqlite",
				"dsn": ":memory:",
				"pool": {
					"maxOpenConns": 1,
					"maxIdleConns": 1,
					"maxConcurrency": 1
				}
			}
		}
	}`), &cfg))

	adapter := NewAdapter[*sqlx.DB]()
	adapter.RegisterDriver("sqlite", &sqliteDriver{})
	defer adapter.Close()

	require.NoError(t, adapter.Configure(cfg))

	err := adapter.Executor().Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	})
	require.NoError(t, err)
}

func TestAdapter_ConfigureErrorNamesSource(t *testing.T) {
	adapter := NewAdapter[*sqlx.DB]()
	defer adapter.Close()

	err := adapter.Configure(Config{
		Sources: map[string]SourceConfig{
			"primary": {Driver: "missing", DSN: ":memory:"},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `configure source "primary"`)
}

func TestAdapter_ConfigureRequiresSourceName(t *testing.T) {
	adapter := NewAdapter[*sqlx.DB]()
	defer adapter.Close()

	err := adapter.Configure(Config{
		Sources: map[string]SourceConfig{
			"": {Driver: "sqlite", DSN: ":memory:"},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "configure source: name is required")
}

func TestAdapter_ConfigureRollsBackOnMidListFailure(t *testing.T) {
	adapter := NewAdapter[*sqlx.DB]()
	adapter.RegisterDriver("sqlite", &sqliteDriver{})
	defer adapter.Close()

	err := adapter.Configure(Config{
		Sources: map[string]SourceConfig{
			"ok-one":     {Driver: "sqlite", DSN: ":memory:"},
			"bad-driver": {Driver: "does-not-exist", DSN: ":memory:"},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `configure source "bad-driver"`)

	// ok-one may have been opened before the failure (map iteration order is
	// random) — either way, Configure must not leave a partially-configured
	// Adapter behind: on error, nothing from this call should remain open.
	err = adapter.Executor().Run(context.Background(), "ok-one", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	require.Error(t, err)
}
