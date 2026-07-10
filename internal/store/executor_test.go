package store

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_Run(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	executor := NewExecutor(pool)

	var called bool
	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestExecutor_Run_NotFound(t *testing.T) {
	pool := newTestPool()
	executor := NewExecutor(pool)

	err := executor.Run(context.Background(), "nonexistent", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	assert.Error(t, err)
}

func TestExecutor_Run_ContextCancelledBeforeRun(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	executor := NewExecutor(pool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var called bool
	err := executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		called = true
		return nil
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, called, "callback must not be called on a cancelled context")
}

func TestExecutor_Run_ThrottleBlocks(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	cfg := testConfig(":memory:")
	cfg.PoolConfig.MaxConcurrency = 1
	require.NoError(t, pool.Register("primary", cfg))

	executor := NewExecutor(pool)

	var running atomic.Bool
	ready := make(chan struct{})

	go func() {
		_ = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
			running.Store(true)
			close(ready)
			time.Sleep(200 * time.Millisecond)
			return nil
		})
	}()

	<-ready
	assert.True(t, running.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecutor_Run_Query(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	executor := NewExecutor(pool)

	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)`)
		return err
	})
	require.NoError(t, err)

	err = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, "hello")
		return err
	})
	require.NoError(t, err)

	err = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		var val string
		err := db.QueryRowContext(ctx, `SELECT val FROM t WHERE id = 1`).Scan(&val)
		if err != nil {
			return err
		}
		assert.Equal(t, "hello", val)
		return nil
	})
	assert.NoError(t, err)
}

func TestExecutor_SQLWorkCanUseTransactionInsideRun(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	executor := NewExecutor(pool)
	ctx := context.Background()

	_ = executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)`)
		return err
	})

	err := runSQLTx(executor, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, "tx-value")
		return err
	})
	require.NoError(t, err)

	_ = executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		var val string
		err := db.QueryRowContext(ctx, `SELECT val FROM t WHERE id = 1`).Scan(&val)
		require.NoError(t, err)
		assert.Equal(t, "tx-value", val)
		return nil
	})
}

func TestExecutor_SQLTransactionRollbackInsideRun(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	executor := NewExecutor(pool)
	ctx := context.Background()

	_ = executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)`)
		return err
	})

	err := runSQLTx(executor, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, "should-rollback")
		return errors.New("intentional error")
	})
	assert.Error(t, err)

	_ = executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count)
		assert.Equal(t, 0, count)
		return nil
	})
}
