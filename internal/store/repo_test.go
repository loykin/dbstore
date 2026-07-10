package store

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// User is an example domain model.
type User struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

// UserRepository is the common contract defined by the application.
// Swap the implementation to test against different DB backends.
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
	CreateBatch(ctx context.Context, names []string) error
}

// sqliteUserRepo is the SQLite implementation using Source[*sqlx.DB].
type sqliteUserRepo struct {
	Source[*sqlx.DB]
	source string
	exec   *Executor[*sqlx.DB]
}

func newUserRepo(exec *Executor[*sqlx.DB]) UserRepository {
	return newUserRepoForSource(exec, "primary")
}

func newUserRepoForSource(exec *Executor[*sqlx.DB], source string) UserRepository {
	return &sqliteUserRepo{
		Source: NewSource(source, exec),
		source: source,
		exec:   exec,
	}
}

func (r *sqliteUserRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *sqliteUserRepo) FindByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &u, `SELECT id, name FROM users WHERE id = ?`, id)
	})
	return &u, err
}

func (r *sqliteUserRepo) FindAll(ctx context.Context) ([]User, error) {
	var users []User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	})
	return users, err
}

// CreateBatch creates multiple users in a single transaction.
func (r *sqliteUserRepo) CreateBatch(ctx context.Context, names []string) error {
	return runSQLTx(r.exec, ctx, r.source, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// setupUserRepoFixture initializes an in-memory SQLite DB with the schema
// for each test and returns a fixture usable by runUserRepoComplianceSuite.
func setupUserRepoFixture(t *testing.T) userRepoFixture {
	t.Helper()
	pool := newTestPool()
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	exec := NewExecutor[*sqlx.DB](pool)

	err := exec.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	})
	require.NoError(t, err)

	return userRepoFixture{
		repo:   newUserRepo(exec),
		exec:   exec,
		source: "primary",
		ph:     func(int) string { return "?" },
	}
}

func TestUserRepoCompliance_SQLite(t *testing.T) {
	runUserRepoComplianceSuite(t, setupUserRepoFixture)
}
