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

func setupStore(ctx context.Context) (UserRepository, func(), error) {
	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	cleanup := sql.Close

	if err := sql.Open("primary", dbstore.SourceConfig{
		Driver:     sqlxadapter.DriverSQLite,
		DSN:        "file:repository?mode=memory&cache=shared",
		PoolConfig: sqlitePoolConfig,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	exec := sql.Executor()
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	repo := NewUserRepo[sqlxadapter.Adaptor](SqliteUserTemplate{}, sql.Source("primary"))
	return repo, cleanup, nil
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
