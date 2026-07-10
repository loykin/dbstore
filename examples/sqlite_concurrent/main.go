package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

func main() {
	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	// MaxConcurrency=1 serializes concurrent writes
	if err := sql.Open("db", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite,
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

	executor := sql.Executor()
	ctx := context.Background()

	if err := executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE log (id INTEGER PRIMARY KEY, msg TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	// 10 goroutines write concurrently — MaxConcurrency=1 serializes them
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			errs <- executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
				_, err := db.ExecContext(ctx, `INSERT INTO log (msg) VALUES (?)`, fmt.Sprintf("goroutine-%d", i))
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			log.Fatal(err)
		}
	}

	if err := executor.Run(ctx, "db", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM log`).Scan(&count); err != nil {
			return err
		}
		fmt.Println("inserted:", count, "rows")
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
