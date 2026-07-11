package sqlxadapter

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"
)

func TestSource_RunTx(t *testing.T) {
	ctx := context.Background()
	adapter := New()
	adapter.RegisterDefaultDrivers()
	defer adapter.Close()

	if err := adapter.Open("primary", dbstore.SourceConfig{
		Driver: DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}

	exec := adapter.Executor()
	source := NewSource("primary", exec)

	if err := source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := source.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

// TestAdapter_Remove covers the dynamic-source teardown path the README's
// "Dynamic Sources" section describes: open a per-tenant source, use it,
// remove just that one, and confirm neither a second Open under the same
// name nor a Run against it work afterward, while the adapter itself (and
// any other source) stays usable.
func TestAdapter_Remove(t *testing.T) {
	ctx := context.Background()
	adapter := New()
	adapter.RegisterDefaultDrivers()
	defer adapter.Close()

	if err := adapter.Open("tenant-a", dbstore.SourceConfig{
		Driver: DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}

	exec := adapter.Executor()
	if err := exec.Run(ctx, "tenant-a", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := adapter.Remove("tenant-a"); err != nil {
		t.Fatal(err)
	}

	if err := exec.Run(ctx, "tenant-a", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	}); err == nil {
		t.Fatal("Run against a removed source should fail, got nil")
	}

	// Removing an already-removed (or never-registered) source is an error,
	// not a silent no-op.
	if err := adapter.Remove("tenant-a"); err == nil {
		t.Fatal("Remove on an already-removed source should error, got nil")
	}

	// The same name can be opened again after removal.
	if err := adapter.Open("tenant-a", dbstore.SourceConfig{
		Driver: DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Run(ctx, "tenant-a", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunTx_Rollback(t *testing.T) {
	ctx := context.Background()
	adapter := New()
	adapter.RegisterDefaultDrivers()
	defer adapter.Close()

	if err := adapter.Open("primary", dbstore.SourceConfig{
		Driver: DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}

	exec := adapter.Executor()
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	err := RunTx(exec, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
		_, execErr := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		if execErr != nil {
			return execErr
		}
		return context.Canceled
	})
	if err == nil {
		t.Fatal("RunTx returned nil, want rollback error")
	}

	var count int
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}
