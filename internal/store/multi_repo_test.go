package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OrderRepository is a second domain repo used in multi-repo tests.
type OrderRepository interface {
	Create(ctx context.Context, userID int, item string) error
	CountByUser(ctx context.Context, userID int) (int, error)
}

type sqliteOrderRepo struct {
	source Source[*sqlx.DB]
}

func newOrderRepo(exec *Executor[*sqlx.DB]) OrderRepository {
	return &sqliteOrderRepo{source: NewSource("orders", exec)}
}

func (r *sqliteOrderRepo) Create(ctx context.Context, userID int, item string) error {
	return r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO orders (user_id, item) VALUES (?, ?)`, userID, item)
		return err
	})
}

func (r *sqliteOrderRepo) CountByUser(ctx context.Context, userID int) (int, error) {
	var count int
	err := r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE user_id = ?`, userID).Scan(&count)
	})
	return count, err
}

// setupMultiRepo registers two independent datasources and returns repos for each.
func setupMultiRepo(t *testing.T) (UserRepository, OrderRepository, *Executor[*sqlx.DB]) {
	t.Helper()
	pool := newTestDirectory()
	t.Cleanup(pool.RemoveAll)

	require.NoError(t, pool.Register("users", testConfig("file:mr_users?mode=memory&cache=shared")))
	require.NoError(t, pool.Register("orders", testConfig("file:mr_orders?mode=memory&cache=shared")))

	exec := NewExecutor(pool)
	ctx := context.Background()

	require.NoError(t, exec.Run(ctx, "users", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	}))
	require.NoError(t, exec.Run(ctx, "orders", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, item TEXT)`)
		return err
	}))

	users := newUserRepoForSource(exec, "users")
	orders := newOrderRepo(exec)
	return users, orders, exec
}

// TestMultiRepo_IndependentDataSources verifies two repos backed by different
// datasources operate independently.
func TestMultiRepo_IndependentDataSources(t *testing.T) {
	users, orders, _ := setupMultiRepo(t)
	ctx := context.Background()

	require.NoError(t, users.Create(ctx, "Alice"))
	require.NoError(t, orders.Create(ctx, 1, "book"))
	require.NoError(t, orders.Create(ctx, 1, "pen"))

	u, err := users.FindByID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Alice", u.Name)

	count, err := orders.CountByUser(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestMultiRepo_ConcurrentWritesDifferentSources fires concurrent writes at
// two datasources simultaneously and verifies correct final counts.
func TestMultiRepo_ConcurrentWritesDifferentSources(t *testing.T) {
	users, orders, _ := setupMultiRepo(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(2)
		i := i
		go func() {
			defer wg.Done()
			_ = users.Create(ctx, fmt.Sprintf("user-%d", i))
		}()
		go func() {
			defer wg.Done()
			_ = orders.Create(ctx, i, fmt.Sprintf("item-%d", i))
		}()
	}
	wg.Wait()

	all, err := users.FindAll(ctx)
	require.NoError(t, err)
	assert.Len(t, all, n)

	// total order count across all user IDs
	var total int
	for i := 0; i < n; i++ {
		c, err := orders.CountByUser(ctx, i)
		require.NoError(t, err)
		total += c
	}
	assert.Equal(t, n, total)
}

// TestMultiRepo_RollbackOnOneSourceDoesNotAffectOther verifies that a
// transaction rollback on one datasource leaves the other datasource intact.
func TestMultiRepo_RollbackOnOneSourceDoesNotAffectOther(t *testing.T) {
	users, orders, exec := setupMultiRepo(t)
	ctx := context.Background()

	require.NoError(t, orders.Create(ctx, 99, "committed"))

	err := runSQLTx(exec, ctx, "users", func(ctx context.Context, tx *sqlx.Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "should-rollback")
		return errors.New("intentional")
	})
	assert.Error(t, err)

	// orders data must be intact
	count, err := orders.CountByUser(ctx, 99)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// users table must be empty
	all, err := users.FindAll(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 0)
}

// TestMultiRepo_TransactionAcrossReposIsIndependent verifies that two
// concurrent transactions on different datasources don't interfere.
func TestMultiRepo_TransactionAcrossReposIsIndependent(t *testing.T) {
	_, _, exec := setupMultiRepo(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = runSQLTx(exec, ctx, "users", func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, "tx-user")
			return err
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = runSQLTx(exec, ctx, "orders", func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO orders (user_id, item) VALUES (?, ?)`, 1, "tx-item")
			return err
		})
	}()
	wg.Wait()

	assert.NoError(t, errs[0])
	assert.NoError(t, errs[1])

	_ = exec.Run(ctx, "users", func(ctx context.Context, db *sqlx.DB) error {
		var c int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&c)
		assert.Equal(t, 1, c)
		return nil
	})

	_ = exec.Run(ctx, "orders", func(ctx context.Context, db *sqlx.DB) error {
		var c int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders`).Scan(&c)
		assert.Equal(t, 1, c)
		return nil
	})
}
