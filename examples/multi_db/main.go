package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

var sqlitePoolConfig = dbstore.PoolConfig{
	MaxOpenConns:   1,
	MaxIdleConns:   1,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 1,
}

func main() {
	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	// meta source: configuration and user data
	if err := sql.Open("meta", dbstore.SourceConfig{
		Driver:     sqlxadapter.DriverSQLite,
		DSN:        "file:meta?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	// stats source: events and aggregated data
	if err := sql.Open("stats", dbstore.SourceConfig{
		Driver:     sqlxadapter.DriverSQLite,
		DSN:        "file:stats?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	executor := sql.Executor()
	ctx := context.Background()

	// initialize meta source
	if err := executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}
	if err := executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return err
	}); err != nil {
		log.Fatal(err)
	}

	// initialize stats source
	if err := executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, type TEXT, count INTEGER)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}
	if err := executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO events (type, count) VALUES (?, ?)`, "login", 42)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	// query each source independently
	if err := executor.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = 1`).Scan(&name); err != nil {
			return err
		}
		fmt.Println("[meta] user:", name)
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	if err := executor.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count FROM events WHERE type = 'login'`).Scan(&count); err != nil {
			return err
		}
		fmt.Println("[stats] login count:", count)
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
