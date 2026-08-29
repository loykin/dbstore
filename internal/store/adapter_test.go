package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

type configureSequenceDriver struct {
	calls         atomic.Int32
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

func (d *configureSequenceDriver) Open(SourceConfig) (*lifecycleTestClient, error) {
	switch d.calls.Add(1) {
	case 2:
		close(d.secondStarted)
		<-d.releaseSecond
		return nil, errors.New("forced failure")
	default:
		return &lifecycleTestClient{}, nil
	}
}

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

func TestAdapter_OpenRequiresSourceName(t *testing.T) {
	adapter := NewAdapter[*sqlx.DB]()
	adapter.RegisterDriver("sqlite", &sqliteDriver{})
	defer adapter.Close()

	err := adapter.Open("", testConfig(":memory:"))
	require.Error(t, err)
	require.ErrorContains(t, err, "source name is required")
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

func TestAdapter_ConfigureRollbackDoesNotRemoveReplacement(t *testing.T) {
	driver := &configureSequenceDriver{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	adapter := NewAdapter[*lifecycleTestClient]()
	adapter.RegisterDriver("sequence", driver)
	defer adapter.Close()
	var releaseOnce atomic.Bool
	releaseFailure := func() {
		if releaseOnce.CompareAndSwap(false, true) {
			close(driver.releaseSecond)
		}
	}
	defer releaseFailure()

	configureDone := make(chan error, 1)
	go func() {
		configureDone <- adapter.Configure(Config{Sources: map[string]SourceConfig{
			"first-map-entry":  {Driver: "sequence"},
			"second-map-entry": {Driver: "sequence"},
		}})
	}()
	<-driver.secondStarted

	// The map iteration order is intentionally irrelevant: whichever source
	// was first is the sole published source while the second Open is blocked.
	sources := adapter.Sources()
	require.Len(t, sources, 1)
	name := sources[0].Name
	require.NoError(t, adapter.Remove(name))
	require.NoError(t, adapter.Open(name, SourceConfig{Driver: "sequence"}))
	replacement, err := adapter.directory.get(name)
	require.NoError(t, err)

	releaseFailure()
	require.Error(t, <-configureDone)
	current, err := adapter.directory.get(name)
	require.NoError(t, err)
	require.Same(t, replacement, current)
}
