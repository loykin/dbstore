package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"
)

type SQLiteDriver struct{}

func (d *SQLiteDriver) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d *SQLiteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	dbstore.DefaultApplyPoolConfig(db, cfg)
}

var sqlitePoolConfig = dbstore.PoolConfig{
	MaxOpenConns:   1,
	MaxIdleConns:   1,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 5,
}

func main() {
	registry := dbstore.NewDriverRegistry()
	registry.Register("sqlite", &SQLiteDriver{})

	pool := dbstore.NewPool(registry)
	defer pool.RemoveAll()

	// meta DB — configuration and user data
	if err := pool.Register("meta", dbstore.DriverConfig{
		Driver:     "sqlite",
		DSN:        "file:meta?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	// stats DB — events and aggregated data
	if err := pool.Register("stats", dbstore.DriverConfig{
		Driver:     "sqlite",
		DSN:        "file:stats?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	executor := dbstore.NewExecutor(pool)
	ctx := context.Background()

	// initialize meta DB
	executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	})
	executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return err
	})

	// initialize stats DB
	executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, type TEXT, count INTEGER)`)
		return err
	})
	executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO events (type, count) VALUES (?, ?)`, "login", 42)
		return err
	})

	// query each DB independently
	executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		var name string
		db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = 1`).Scan(&name)
		fmt.Println("[meta] user:", name)
		return nil
	})

	executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		db.QueryRowContext(ctx, `SELECT count FROM events WHERE type = 'login'`).Scan(&count)
		fmt.Println("[stats] login count:", count)
		return nil
	})
}
