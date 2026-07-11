package store

import (
	"context"
	"fmt"
	"time"
)

type Executor[T any] struct {
	directory *Directory[T]
}

func NewExecutor[T any](directory *Directory[T]) *Executor[T] {
	return &Executor[T]{directory: directory}
}

// Run executes fn against the client registered under name.
// Sequence: acquire throttle → run fn → release throttle.
// A cancelled ctx returns immediately at Acquire to prevent goroutine leaks.
// If an Observer is set on the Directory, ObserveAcquire and ObserveComplete
// bracket the throttle wait and fn's execution respectively — see Observer's
// doc comment for how that pairing supports an in-flight gauge. ObserveComplete
// is called via defer specifically so a panic inside fn still decrements that
// gauge instead of leaking it — an Observer that reports a value it can never
// walk back is worse than no Observer, since it looks trustworthy right up
// until it silently isn't.
func (e *Executor[T]) Run(ctx context.Context, name string, fn func(context.Context, T) error) (err error) {
	entry, acquireErr := e.directory.acquire(name)
	if acquireErr != nil {
		return acquireErr
	}
	defer e.directory.release(entry)

	observer := e.directory.getObserver()

	waitStart := time.Now()
	if throttleErr := entry.throttle.Acquire(ctx); throttleErr != nil {
		if observer != nil {
			safeObserve(func() { observer.ObserveAcquire(name, time.Since(waitStart), throttleErr) })
		}
		return throttleErr
	}
	if observer != nil {
		safeObserve(func() { observer.ObserveAcquire(name, time.Since(waitStart), nil) })
	}
	defer entry.throttle.Release()

	runStart := time.Now()
	if observer != nil {
		defer func() {
			if r := recover(); r != nil {
				safeObserve(func() { observer.ObserveComplete(name, time.Since(runStart), fmt.Errorf("panic: %v", r)) })
				panic(r)
			}
			safeObserve(func() { observer.ObserveComplete(name, time.Since(runStart), err) })
		}()
	}
	err = fn(ctx, entry.client)
	return err
}
