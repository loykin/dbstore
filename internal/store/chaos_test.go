package store

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

// TestPool_Chaos_GoroutineLeaks runs multiple register/remove/run cycles and
// verifies no goroutines are leaked. Set DBSTORE_CHAOS_TEST=1 to enable.
func TestPool_Chaos_GoroutineLeaks(t *testing.T) {
	if os.Getenv("DBSTORE_CHAOS_TEST") == "" {
		t.Skip("set DBSTORE_CHAOS_TEST=1 to enable")
	}

	initial := runtime.NumGoroutine()
	t.Logf("initial goroutines: %d", initial)

	const cycles = 20
	for cycle := 0; cycle < cycles; cycle++ {
		pool := newTestPool()

		cfg := testConfig(fmt.Sprintf("file:chaos_%d?mode=memory&cache=shared", cycle))
		cfg.PoolConfig.MaxConcurrency = 5
		if err := pool.Register("primary", cfg); err != nil {
			t.Fatalf("cycle %d: register: %v", cycle, err)
		}

		exec := NewExecutor(pool)
		ctx := context.Background()

		_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)`)
			return err
		})

		const workers = 10
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
					time.Sleep(5 * time.Millisecond)
					_, err := db.ExecContext(ctx, `INSERT INTO t VALUES (NULL)`)
					return err
				})
			}()
		}
		wg.Wait()
		pool.RemoveAll()

		// allow goroutines to settle
		time.Sleep(50 * time.Millisecond)
		runtime.GC()

		current := runtime.NumGoroutine()
		leaked := current - initial
		t.Logf("cycle %d: goroutines=%d (leak=%+d)", cycle+1, current, leaked)
		if leaked > 10 {
			t.Errorf("cycle %d: goroutine leak detected: %d extra goroutines", cycle+1, leaked)
		}
	}

	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	final := runtime.NumGoroutine() - initial
	t.Logf("final goroutine delta: %+d", final)
	assert.LessOrEqual(t, final, 10, "persistent goroutine leak after all cycles")
}

// TestPool_Chaos_MemoryStability runs sustained concurrent load and verifies
// memory usage stays bounded. Set DBSTORE_CHAOS_TEST=1 to enable.
func TestPool_Chaos_MemoryStability(t *testing.T) {
	if os.Getenv("DBSTORE_CHAOS_TEST") == "" {
		t.Skip("set DBSTORE_CHAOS_TEST=1 to enable")
	}

	duration := parseChaosEnv("DBSTORE_CHAOS_DURATION", 2*time.Minute)
	t.Logf("running memory stability test for %v", duration)

	pool := newTestPool()
	defer pool.RemoveAll()

	const dbCount = 3
	for i := 0; i < dbCount; i++ {
		name := fmt.Sprintf("db%d", i)
		dsn := fmt.Sprintf("file:mem_%s?mode=memory&cache=shared", name)
		if err := pool.Register(name, testConfig(dsn)); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	exec := NewExecutor(pool)
	ctx := context.Background()

	for i := 0; i < dbCount; i++ {
		name := fmt.Sprintf("db%d", i)
		_ = exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)`)
			return err
		})
	}

	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	var totalOps atomic.Int64
	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < dbCount; i++ {
		for w := 0; w < 3; w++ {
			wg.Add(1)
			name := fmt.Sprintf("db%d", i)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stopCh:
						return
					default:
						_ = exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
							_, err := db.ExecContext(ctx, `INSERT INTO t (val) VALUES (?)`, "chaos")
							return err
						})
						totalOps.Add(1)
					}
				}
			}()
		}
	}

	ticker := time.NewTicker(30 * time.Second)
	timer := time.NewTimer(duration)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			goto done
		case <-ticker.C:
			var cur runtime.MemStats
			runtime.ReadMemStats(&cur)
			growth := int64(cur.HeapAlloc) - int64(baseline.HeapAlloc)
			t.Logf("ops=%d  heap=%dMB  growth=%+dMB  gc=%d",
				totalOps.Load(),
				cur.HeapAlloc/1024/1024,
				growth/1024/1024,
				cur.NumGC,
			)
			if growth > 200*1024*1024 {
				t.Errorf("heap grew by %dMB — possible memory leak", growth/1024/1024)
			}
		}
	}

done:
	close(stopCh)
	wg.Wait()

	runtime.GC()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	growth := int64(final.HeapAlloc) - int64(baseline.HeapAlloc)
	t.Logf("total ops: %d  final growth: %+dMB  gc cycles: %d",
		totalOps.Load(), growth/1024/1024, final.NumGC-baseline.NumGC)

	assert.Less(t, growth, int64(200*1024*1024), "heap growth must stay under 200MB")
}

func parseChaosEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
