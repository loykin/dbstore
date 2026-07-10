package store

import "context"

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
