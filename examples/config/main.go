package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed config.json
var configJSON []byte

type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (string, error)
}

type userRepo struct {
	sqlxadapter.Source
}

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) UserRepository {
	return &userRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *userRepo) FindByID(ctx context.Context, id int) (string, error) {
	var name string
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, id).Scan(&name)
	})
	return name, err
}

type StatsRepository interface {
	RecordLogin(ctx context.Context, count int) error
	LoginCount(ctx context.Context) (int, error)
}

type statsRepo struct {
	sqlxadapter.Source
}

func NewStatsRepo(exec *dbstore.Executor[*sqlx.DB], source string) StatsRepository {
	return &statsRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *statsRepo) RecordLogin(ctx context.Context, count int) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO events (type, count) VALUES (?, ?)`, "login", count)
		return err
	})
}

func (r *statsRepo) LoginCount(ctx context.Context) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT count FROM events WHERE type = 'login'`).Scan(&count)
	})
	return count, err
}

func main() {
	ctx := context.Background()

	// dbstore does not load JSON/YAML itself — the application decodes it
	// into dbstore.Config and hands it to the adapter.
	var cfg dbstore.Config
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		log.Fatal(err)
	}

	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	// Configure opens every source in cfg.Sources in one call. If any
	// source fails to open, sources already opened by this call are closed
	// again before the error is returned — Configure is all-or-nothing.
	if err := sql.Configure(cfg); err != nil {
		log.Fatal(err)
	}

	exec := sql.Executor()

	// "meta" and "stats" are names the application already knows at compile
	// time (see config.json) — config only externalizes *how* to open each
	// source (driver/DSN/pool), not *what they're called*. Repositories
	// embed sqlxadapter.Source the same way they would if the source had
	// been opened with a single Open call instead of Configure.
	if err := exec.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}
	if err := exec.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, type TEXT, count INTEGER)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	users := NewUserRepo(exec, "meta")
	stats := NewStatsRepo(exec, "stats")

	if err := users.Create(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}
	if err := stats.RecordLogin(ctx, 42); err != nil {
		log.Fatal(err)
	}

	name, err := users.FindByID(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[meta] user:", name)

	count, err := stats.LoginCount(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[stats] login count:", count)
}
