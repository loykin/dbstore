package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
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

// sqliteUserRepo is the SQLite implementation using BaseRepo.
type sqliteUserRepo struct {
	BaseRepo
}

func newUserRepo(exec *Executor) UserRepository {
	return &sqliteUserRepo{NewBaseRepo("primary", exec)}
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
	return r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// setupUserRepo initializes an in-memory SQLite DB with the schema for each test.
func setupUserRepo(t *testing.T) UserRepository {
	t.Helper()
	pool := newTestPool()
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	exec := NewExecutor(pool)

	err := exec.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	})
	require.NoError(t, err)

	return newUserRepo(exec)
}

func TestUserRepo_Create_FindByID(t *testing.T) {
	repo := setupUserRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, "Alice"))

	u, err := repo.FindByID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Alice", u.Name)
}

func TestUserRepo_FindAll(t *testing.T) {
	repo := setupUserRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, "Alice"))
	require.NoError(t, repo.Create(ctx, "Bob"))

	users, err := repo.FindAll(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
}

func TestUserRepo_FindByID_NotFound(t *testing.T) {
	repo := setupUserRepo(t)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, 999)
	assert.Error(t, err)
}

func TestUserRepo_CreateBatch_Commit(t *testing.T) {
	repo := setupUserRepo(t)
	ctx := context.Background()

	err := repo.CreateBatch(ctx, []string{"Alice", "Bob", "Carol"})
	require.NoError(t, err)

	users, err := repo.FindAll(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 3)
}

func TestUserRepo_CreateBatch_Rollback(t *testing.T) {
	pool := newTestPool()
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	exec := NewExecutor(pool)
	ctx := context.Background()

	exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	})

	// mid-transaction failure → full rollback
	concrete := &sqliteUserRepo{NewBaseRepo("primary", exec)}
	err := concrete.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "Alice")
		return errors.New("intentional error")
	})
	assert.Error(t, err)

	repo := newUserRepo(exec)
	users, err := repo.FindAll(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 0)
}
