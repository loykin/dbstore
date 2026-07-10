package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

var singleConnPoolConfig = dbstore.PoolConfig{
	MaxOpenConns:   1,
	MaxIdleConns:   1,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 1,
}

func main() {
	ctx := context.Background()

	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	if err := sql.Open("local", dbstore.SourceConfig{
		Driver:     sqlxadapter.DriverSQLite,
		DSN:        ":memory:",
		PoolConfig: singleConnPoolConfig,
	}); err != nil {
		log.Fatal(err)
	}

	if postgresDSN := os.Getenv("POSTGRES_DSN"); postgresDSN != "" {
		if err := sql.Open("warehouse", dbstore.SourceConfig{
			Driver:     sqlxadapter.DriverPostgres,
			DSN:        postgresDSN,
			PoolConfig: dbstore.DefaultPoolConfig,
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("registered sqlite source local and postgres source warehouse")
	} else {
		fmt.Println("registered sqlite source local; set POSTGRES_DSN to open postgres too")
	}

	if err := sql.Executor().Run(ctx, "local", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return err
	}); err != nil {
		log.Fatal(err)
	}
}
