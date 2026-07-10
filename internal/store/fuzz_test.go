package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// FuzzDirectory_Register verifies that arbitrary name strings never cause a panic
// in Register or Remove, regardless of content.
func FuzzDirectory_Register(f *testing.F) {
	f.Add("primary")
	f.Add("")
	f.Add("a b")
	f.Add("a/b")
	f.Add("'; DROP TABLE users; --")
	f.Add(strings.Repeat("x", 512))

	f.Fuzz(func(t *testing.T, name string) {
		pool := newTestDirectory()
		defer pool.RemoveAll()

		_ = pool.Register(name, testConfig(":memory:"))
		_ = pool.Remove(name)
	})
}

// FuzzDirectory_AcquireRelease verifies that acquire/release cycles with arbitrary
// names never panic even when the entry does not exist.
func FuzzDirectory_AcquireRelease(f *testing.F) {
	f.Add("primary", "missing")
	f.Add("a", "a")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, registerName, acquireName string) {
		pool := newTestDirectory()
		defer pool.RemoveAll()

		_ = pool.Register(registerName, testConfig(":memory:"))

		entry, err := pool.acquire(acquireName)
		if err == nil {
			pool.release(entry)
		}
	})
}

// FuzzExecutor_Run verifies that arbitrary names and query strings never cause
// a panic in Executor.Run.
func FuzzExecutor_Run(f *testing.F) {
	f.Add("primary", "SELECT 1")
	f.Add("primary", "")
	f.Add("missing", "SELECT 1")
	f.Add("primary", "NOT VALID SQL 🔥")

	f.Fuzz(func(t *testing.T, name, query string) {
		pool := newTestDirectory()
		defer pool.RemoveAll()

		_ = pool.Register("primary", testConfig(":memory:"))
		exec := NewExecutor(pool)

		_ = exec.Run(context.Background(), name, func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, query)
			return err
		})
	})
}

// FuzzThrottle_Concurrency verifies that newThrottle with arbitrary limits
// never panics, including negative and zero values.
func FuzzThrottle_Concurrency(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(-1)
	f.Add(1000)
	f.Add(-999)

	f.Fuzz(func(t *testing.T, limit int) {
		th := newThrottle(limit)

		ctx := context.Background()
		if err := th.Acquire(ctx); err == nil {
			th.Release()
		}
	})
}
