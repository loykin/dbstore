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

func (d *SQLiteDriver) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d *SQLiteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}

var sqlitePoolConfig = dbstore.PoolConfig{
	MaxOpenConns:   1,
	MaxIdleConns:   1,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 1,
}

type User struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

type UserRepository interface {
	Create(ctx context.Context, name string) error
	CreateBatch(ctx context.Context, names []string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
}

type sqliteUserRepo struct {
	sqlxadapter.Source
}

var _ UserRepository = (*sqliteUserRepo)(nil)

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) UserRepository {
	return &sqliteUserRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *sqliteUserRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *sqliteUserRepo) CreateBatch(ctx context.Context, names []string) error {
	return r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *sqliteUserRepo) FindByID(ctx context.Context, id int) (*User, error) {
	var user User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &user, `SELECT id, name FROM users WHERE id = ?`, id)
	})
	return &user, err
}

func (r *sqliteUserRepo) FindAll(ctx context.Context) ([]User, error) {
	var users []User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	})
	return users, err
}

func setupStore(ctx context.Context) (UserRepository, func(), error) {
	registry := dbstore.NewDriverRegistry[*sqlx.DB]()
	registry.Register("sqlite", &SQLiteDriver{})

	pool := dbstore.NewPool(registry)
	cleanup := pool.RemoveAll

	if err := pool.Register("primary", dbstore.DriverConfig{
		Driver:     "sqlite",
		DSN:        "file:repository?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	exec := dbstore.NewExecutor(pool)
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	return NewUserRepo(exec, "primary"), cleanup, nil
}

func main() {
	ctx := context.Background()
	repo, cleanup, err := setupStore(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	if err := repo.Create(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}
	if err := repo.CreateBatch(ctx, []string{"Bob", "Carol"}); err != nil {
		log.Fatal(err)
	}

	users, err := repo.FindAll(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range users {
		fmt.Printf("%d: %s\n", user.ID, user.Name)
	}
}
