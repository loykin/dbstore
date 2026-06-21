package store

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type Executor struct {
	pool *Pool
}

func NewExecutor(pool *Pool) *Executor {
	return &Executor{pool: pool}
}

// Run executes fn against the DB registered under name.
// Sequence: acquire throttle → run fn → release throttle.
// A cancelled ctx returns immediately at Acquire to prevent goroutine leaks.
func (e *Executor) Run(ctx context.Context, name string, fn func(context.Context, *sqlx.DB) error) error {
	entry, err := e.pool.acquire(name)
	if err != nil {
		return err
	}
	defer e.pool.release(entry)

	if err := entry.throttle.Acquire(ctx); err != nil {
		return err
	}
	defer entry.throttle.Release()
	return fn(ctx, entry.db)
}

// RunTx executes fn within a transaction.
// Commits on success; rolls back automatically on error or panic.
func (e *Executor) RunTx(ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	entry, err := e.pool.acquire(name)
	if err != nil {
		return err
	}
	defer e.pool.release(entry)

	if err := entry.throttle.Acquire(ctx); err != nil {
		return err
	}
	defer entry.throttle.Release()

	tx, err := entry.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit; also covers panics

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
