package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPool_Stress_RemoveWaitsForInFlight verifies that Remove blocks until
// all in-flight operations complete before closing the connection (P1 fix).
func TestPool_Stress_RemoveWaitsForInFlight(t *testing.T) {
	pool := newTestPool()

	cfg := testConfig(":memory:")
	cfg.PoolConfig.MaxConcurrency = 10
	require.NoError(t, pool.Register("primary", cfg))

	exec := NewExecutor(pool)
	ctx := context.Background()

	const workers = 10
	allInside := make(chan struct{})
	var insideCount atomic.Int32
	var completed atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
				if insideCount.Add(1) == workers {
					close(allInside)
				}
				time.Sleep(100 * time.Millisecond)
				completed.Add(1)
				return nil
			})
		}()
	}

	<-allInside // all workers are inside the callback
	_ = pool.Remove("primary")

	assert.Equal(t, int32(workers), completed.Load(), "Remove returned before all in-flight ops finished")
	wg.Wait()
}

// TestPool_Stress_ConcurrentRegisterSameName verifies that only one concurrent
// Register for the same name succeeds (double-check locking).
func TestPool_Stress_ConcurrentRegisterSameName(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	const goroutines = 30
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pool.Register("primary", testConfig(":memory:")) == nil {
				successCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), successCount.Load(), "exactly one Register should succeed")
}

// TestExecutor_Stress_ConcurrentRun hammers the executor with many goroutines
// and verifies no writes are lost.
func TestExecutor_Stress_ConcurrentRun(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	cfg := testConfig("file:stress_exec?mode=memory&cache=shared")
	cfg.PoolConfig.MaxConcurrency = 5
	require.NoError(t, pool.Register("primary", cfg))

	exec := NewExecutor(pool)
	ctx := context.Background()

	require.NoError(t, exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, val INTEGER)`)
		return err
	}))

	const workers = 50
	var wg sync.WaitGroup
	var errCount atomic.Int32

	for i := 0; i < workers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
				_, err := db.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, i)
				return err
			}); err != nil {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), errCount.Load())

	_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		var count int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count)
		assert.Equal(t, workers, count)
		return nil
	})
}

// TestExecutor_Stress_MultipleDataSources registers N datasources and fires
// concurrent writes at all of them, verifying per-datasource row counts.
func TestExecutor_Stress_MultipleDataSources(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	const dbCount = 5
	const writesPerDB = 20

	for i := 0; i < dbCount; i++ {
		name := fmt.Sprintf("db%d", i)
		dsn := fmt.Sprintf("file:stress_multi_%s?mode=memory&cache=shared", name)
		require.NoError(t, pool.Register(name, testConfig(dsn)))
	}

	exec := NewExecutor(pool)
	ctx := context.Background()

	for i := 0; i < dbCount; i++ {
		name := fmt.Sprintf("db%d", i)
		require.NoError(t, exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, val INTEGER)`)
			return err
		}))
	}

	var wg sync.WaitGroup
	var errCount atomic.Int32

	for i := 0; i < dbCount; i++ {
		for j := 0; j < writesPerDB; j++ {
			wg.Add(1)
			name := fmt.Sprintf("db%d", i)
			val := j
			go func() {
				defer wg.Done()
				if err := exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
					_, err := db.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, val)
					return err
				}); err != nil {
					errCount.Add(1)
				}
			}()
		}
	}
	wg.Wait()

	assert.Equal(t, int32(0), errCount.Load())

	for i := 0; i < dbCount; i++ {
		name := fmt.Sprintf("db%d", i)
		_ = exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
			var count int
			_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count)
			assert.Equal(t, writesPerDB, count, "%s: expected %d rows", name, writesPerDB)
			return nil
		})
	}
}

// TestExecutor_Stress_ThrottleUnderLoad verifies the per-datasource semaphore
// correctly limits concurrency under high goroutine count.
func TestExecutor_Stress_ThrottleUnderLoad(t *testing.T) {
	pool := newTestPool()
	defer pool.RemoveAll()

	const limit = 3
	cfg := testConfig(":memory:")
	cfg.PoolConfig.MaxConcurrency = limit
	require.NoError(t, pool.Register("primary", cfg))

	exec := NewExecutor(pool)
	ctx := context.Background()

	var active atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
				cur := active.Add(1)
				for {
					old := maxActive.Load()
					if cur <= old || maxActive.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, maxActive.Load(), int32(limit))
}
