package store

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type Executor[T any] struct {
	pool *Pool[T]
}

func NewExecutor[T any](pool *Pool[T]) *Executor[T] {
	return &Executor[T]{pool: pool}
}

// Run executes fn against the client registered under name.
// Sequence: acquire throttle → run fn → release throttle.
// A cancelled ctx returns immediately at Acquire to prevent goroutine leaks.
func (e *Executor[T]) Run(ctx context.Context, name string, fn func(context.Context, T) error) error {
	entry, err := e.pool.acquire(name)
	if err != nil {
		return err
	}
	defer e.pool.release(entry)

	if err := entry.throttle.Acquire(ctx); err != nil {
		return err
	}
	defer entry.throttle.Release()
	return fn(ctx, entry.client)
}

// RunTx executes fn within a transaction.
// Commits on success; rolls back automatically on error or panic.
//
// This is a package-level function constrained to *sqlx.DB, not an
// Executor[T] method: Go generics can't express a method that only exists
// for one type argument, and transactions are a *sqlx.DB-specific concept
// that doesn't generalize to non-SQL clients.
func RunTx(e *Executor[*sqlx.DB], ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	entry, err := e.pool.acquire(name)
	if err != nil {
		return err
	}
	defer e.pool.release(entry)

	if err := entry.throttle.Acquire(ctx); err != nil {
		return err
	}
	defer entry.throttle.Release()

	tx, err := entry.client.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit; also covers panics

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
