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

type SQLiteDriver struct{}

func (d *SQLiteDriver) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d *SQLiteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	sqlxadapter.ApplyPoolConfig(db, cfg)
}

var sqlitePoolConfig = dbstore.PoolConfig{
	MaxOpenConns:   1,
	MaxIdleConns:   1,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 1,
}

func main() {
	sql := sqlxadapter.New()
	sql.RegisterDriver("sqlite", &SQLiteDriver{})
	defer sql.Close()

	if err := sql.Open("main", dbstore.SourceConfig{
		Driver:     "sqlite",
		DSN:        ":memory:",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	executor := sql.Executor()
	ctx := context.Background()

	if err := executor.Run(ctx, "main", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	if err := executor.Run(ctx, "main", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return err
	}); err != nil {
		log.Fatal(err)
	}

	if err := executor.Run(ctx, "main", func(ctx context.Context, db *sqlx.DB) error {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = 1`).Scan(&name)
		if err != nil {
			return err
		}
		fmt.Println("name:", name)
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
