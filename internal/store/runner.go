package store

import (
	"context"
	"errors"
)

// Runner is the minimal capability a domain repository needs from a
// backend handle: run a function against a client of type T. Source[T]
// satisfies it already; adapter packages expose their own Runner via a
// backend-specific Handle type instead of a raw client.
type Runner[T any] interface {
	Run(ctx context.Context, fn func(context.Context, T) error) error
}

// ErrNotFound is the sentinel a Backend implementation returns to signal
// "no such record" for any backend (a SQL sql.ErrNoRows, an HTTP 404, a
// search-engine miss, ...). Call translates it into a (zero, nil) result,
// so every domain repository method gets the same not-found contract
// regardless of backend, instead of each Backend author having to
// remember to do the translation themselves.
var ErrNotFound = errors.New("dbstore: not found")

// Exec runs fn against src's client for the error-only method shape.
func Exec[T any](ctx context.Context, src Runner[T], fn func(context.Context, T) error) error {
	return src.Run(ctx, fn)
}

// Call runs fn against src's client for the (value, error) method shape.
// If fn (or anything it calls) returns an error wrapping ErrNotFound, Call
// translates it into a (zero value, nil) result — the not-found contract
// every domain repository method shares, enforced in one place instead of
// by convention in every Backend implementation.
func Call[T, R any](ctx context.Context, src Runner[T], fn func(context.Context, T) (R, error)) (R, error) {
	var result R
	err := src.Run(ctx, func(ctx context.Context, c T) error {
		var e error
		result, e = fn(ctx, c)
		return e
	})
	if errors.Is(err, ErrNotFound) {
		var zero R
		return zero, nil
	}
	return result, err
}
