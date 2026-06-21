//go:build integration

package store

import (
	"context"
	"fmt"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// --- driver implementations ---

type postgresDriver struct{}

func (d *postgresDriver) Open(cfg DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("postgres", cfg.DSN)
}

func (d *postgresDriver) ApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	DefaultApplyPoolConfig(db, cfg)
}

type mysqlDriver struct{}

func (d *mysqlDriver) Open(cfg DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("mysql", cfg.DSN)
}

func (d *mysqlDriver) ApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	DefaultApplyPoolConfig(db, cfg)
}

// --- shared suite ---

// containerSuite runs the standard set of pool/executor/repo tests against
// any real database. placeholder is "?" for MySQL/SQLite, "$" for PostgreSQL.
func containerSuite(t *testing.T, exec *Executor, source, ph string) {
	t.Helper()
	ctx := context.Background()

	p := func(n int) string {
		if ph == "$" {
			return fmt.Sprintf("$%d", n)
		}
		return "?"
	}

	// schema
	require.NoError(t, exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cs_users (
			id   SERIAL PRIMARY KEY,
			name TEXT NOT NULL
		)`)
		return err
	}))

	t.Run("Insert_and_Select", func(t *testing.T) {
		require.NoError(t, exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `INSERT INTO cs_users (name) VALUES (`+p(1)+`)`, "Alice")
			return err
		}))

		var name string
		require.NoError(t, exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT name FROM cs_users LIMIT 1`).Scan(&name)
		}))
		assert.Equal(t, "Alice", name)
	})

	t.Run("Transaction_Commit", func(t *testing.T) {
		require.NoError(t, exec.RunTx(ctx, source, func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO cs_users (name) VALUES (`+p(1)+`)`, "Bob")
			return err
		}))

		var count int
		exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cs_users WHERE name = 'Bob'`).Scan(&count)
		})
		assert.Equal(t, 1, count)
	})

	t.Run("Transaction_Rollback", func(t *testing.T) {
		before := 0
		exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cs_users`).Scan(&before)
		})

		err := exec.RunTx(ctx, source, func(ctx context.Context, tx *sqlx.Tx) error {
			tx.ExecContext(ctx, `INSERT INTO cs_users (name) VALUES (`+p(1)+`)`, "ShouldRollback")
			return fmt.Errorf("intentional")
		})
		require.Error(t, err)

		var after int
		exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cs_users`).Scan(&after)
		})
		assert.Equal(t, before, after, "rollback must not change row count")
	})

	t.Run("Concurrent_Inserts", func(t *testing.T) {
		before := 0
		exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cs_users`).Scan(&before)
		})

		const workers = 10
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			i := i
			go func() {
				errs <- exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
					_, err := db.ExecContext(ctx, `INSERT INTO cs_users (name) VALUES (`+p(1)+`)`,
						fmt.Sprintf("concurrent-%d", i))
					return err
				})
			}()
		}
		for i := 0; i < workers; i++ {
			assert.NoError(t, <-errs)
		}

		var after int
		exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
			return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cs_users`).Scan(&after)
		})
		assert.Equal(t, before+workers, after)
	})
}

// --- PostgreSQL ---

func TestContainer_Postgres(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("user"),
		tcpostgres.WithPassword("pass"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	registry := NewDriverRegistry()
	registry.Register("postgres", &postgresDriver{})

	pool := NewPool(registry)
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", DriverConfig{
		Driver:     "postgres",
		DSN:        dsn,
		PoolConfig: DefaultPoolConfig,
	}))

	exec := NewExecutor(pool)
	containerSuite(t, exec, "primary", "$")
}

// --- MySQL ---

func TestContainer_MySQL(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase("testdb"),
		tcmysql.WithUsername("user"),
		tcmysql.WithPassword("pass"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)

	registry := NewDriverRegistry()
	registry.Register("mysql", &mysqlDriver{})

	pool := NewPool(registry)
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", DriverConfig{
		Driver:     "mysql",
		DSN:        dsn,
		PoolConfig: DefaultPoolConfig,
	}))

	exec := NewExecutor(pool)
	containerSuite(t, exec, "primary", "?")
}

// --- Multi-DB (Postgres + MySQL simultaneously) ---

func TestContainer_MultiDB_PostgresAndMySQL(t *testing.T) {
	ctx := context.Background()

	pgCtr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("user"),
		tcpostgres.WithPassword("pass"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { pgCtr.Terminate(ctx) })

	mysCtr, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase("testdb"),
		tcmysql.WithUsername("user"),
		tcmysql.WithPassword("pass"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { mysCtr.Terminate(ctx) })

	pgDSN, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	mysDSN, err := mysCtr.ConnectionString(ctx)
	require.NoError(t, err)

	registry := NewDriverRegistry()
	registry.Register("postgres", &postgresDriver{})
	registry.Register("mysql", &mysqlDriver{})

	pool := NewPool(registry)
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("pg", DriverConfig{Driver: "postgres", DSN: pgDSN, PoolConfig: DefaultPoolConfig}))
	require.NoError(t, pool.Register("my", DriverConfig{Driver: "mysql", DSN: mysDSN, PoolConfig: DefaultPoolConfig}))

	exec := NewExecutor(pool)

	// run the same suite against both at the same time
	pgDone := make(chan struct{})
	myDone := make(chan struct{})

	go func() {
		defer close(pgDone)
		containerSuite(t, exec, "pg", "$")
	}()
	go func() {
		defer close(myDone)
		containerSuite(t, exec, "my", "?")
	}()

	<-pgDone
	<-myDone
}
