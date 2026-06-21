package main

import (
	"context"
	"fmt"
	"log"
	"sync"
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

func main() {
	registry := dbstore.NewDriverRegistry()
	registry.Register("sqlite", &SQLiteDriver{})

	pool := dbstore.NewPool(registry)
	defer pool.RemoveAll()

	// MaxConcurrency=1 serializes concurrent writes
	if err := pool.Register("db", dbstore.DriverConfig{
		Driver: "sqlite",
		DSN:    "file:concurrent?mode=memory&cache=shared",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxLifetime:    30 * time.Minute,
			MaxIdleTime:    5 * time.Minute,
			MaxConcurrency: 1,
		},
	}); err != nil {
		log.Fatal(err)
	}

	executor := dbstore.NewExecutor(pool)
	ctx := context.Background()

	executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE log (id INTEGER PRIMARY KEY, msg TEXT)`)
		return err
	})

	// 10 goroutines write concurrently — MaxConcurrency=1 serializes them
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
				_, err := db.ExecContext(ctx, `INSERT INTO log (msg) VALUES (?)`, fmt.Sprintf("goroutine-%d", i))
				return err
			})
		}()
	}
	wg.Wait()

	executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		db.QueryRowContext(ctx, `SELECT COUNT(*) FROM log`).Scan(&count)
		fmt.Println("inserted:", count, "rows")
		return nil
	})
}
