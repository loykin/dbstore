//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// postgresUserRepo is the PostgreSQL implementation of UserRepository, used
// to run the same compliance suite SQLite runs against a real backend with
// different placeholder/autoincrement semantics.
type postgresUserRepo struct {
	runtimeSource Source[*sqlx.DB]
	source        string
	exec          *Executor[*sqlx.DB]
}

func newPostgresUserRepo(exec *Executor[*sqlx.DB]) UserRepository {
	return &postgresUserRepo{
		runtimeSource: NewSource("primary", exec),
		source:        "primary",
		exec:          exec,
	}
}

func (r *postgresUserRepo) Create(ctx context.Context, name string) error {
	return r.runtimeSource.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES ($1)`, name)
		return err
	})
}

func (r *postgresUserRepo) FindByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := r.runtimeSource.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &u, `SELECT id, name FROM users WHERE id = $1`, id)
	})
	return &u, err
}

func (r *postgresUserRepo) FindAll(ctx context.Context) ([]User, error) {
	var users []User
	err := r.runtimeSource.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	})
	return users, err
}

func (r *postgresUserRepo) CreateBatch(ctx context.Context, names []string) error {
	return runSQLTx(r.exec, ctx, r.source, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES ($1)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// TestUserRepoCompliance_Postgres starts ONE Postgres container and reuses
// it across all compliance subtests — runUserRepoComplianceSuite calls its
// setup closure once per t.Run, so re-provisioning a container there (as an
// earlier version of this test did) meant 5 containers per test run instead
// of 1. Each subtest still gets an isolated, empty "users" table by
// dropping and recreating it, which is what actually needs to be fresh per
// subtest — not the container or connection pool underneath it.
func TestUserRepoCompliance_Postgres(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("user"),
		tcpostgres.WithPassword("pass"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(60*time.Second),
			),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	registry := NewDriverRegistry[*sqlx.DB]()
	registry.Register("postgres", &postgresDriver{})

	pool := NewDirectory(registry)
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", SourceConfig{
		Driver:     "postgres",
		DSN:        dsn,
		PoolConfig: DefaultPoolConfig,
	}))

	exec := NewExecutor(pool)

	runUserRepoComplianceSuite(t, func(t *testing.T) userRepoFixture {
		t.Helper()
		require.NoError(t, exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS users`)
			if err != nil {
				return err
			}
			_, err = db.ExecContext(ctx, `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`)
			return err
		}))

		return userRepoFixture{
			repo:   newPostgresUserRepo(exec),
			exec:   exec,
			source: "primary",
			ph:     func(n int) string { return "$1" },
		}
	})
}
