package sqlxadapter

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"
)

type sqliteDriver struct{}

func (d sqliteDriver) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d sqliteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	ApplyPoolConfig(db, cfg)
}

func TestSource_RunTx(t *testing.T) {
	ctx := context.Background()
	adapter := New()
	adapter.RegisterDriver("sqlite", sqliteDriver{})
	defer adapter.Close()

	if err := adapter.Open("primary", dbstore.SourceConfig{
		Driver: "sqlite",
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

func TestRunTx_Rollback(t *testing.T) {
	ctx := context.Background()
	adapter := New()
	adapter.RegisterDriver("sqlite", sqliteDriver{})
	defer adapter.Close()

	if err := adapter.Open("primary", dbstore.SourceConfig{
		Driver: "sqlite",
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
