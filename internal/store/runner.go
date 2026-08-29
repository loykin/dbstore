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
// search-engine miss, ...). Call returns it with the result type's zero value,
// giving every generated repository one consistent not-found rule.
var ErrNotFound = errors.New("dbstore: not found")

// Exec runs fn against src's client for the error-only method shape.
func Exec[T any](ctx context.Context, src Runner[T], fn func(context.Context, T) error) error {
	return src.Run(ctx, fn)
}

// Call runs fn against src's client for the (value, error) method shape
// without hiding errors. ErrNotFound is returned with R's zero value so a
// Backend cannot accidentally leak a partially-filled result on a miss. Other
// errors preserve the Backend's result.
func Call[T, R any](ctx context.Context, src Runner[T], fn func(context.Context, T) (R, error)) (R, error) {
	var result R
	err := src.Run(ctx, func(ctx context.Context, c T) error {
		var e error
		result, e = fn(ctx, c)
		return e
	})
	if errors.Is(err, ErrNotFound) {
		var zero R
		return zero, err
	}
	return result, err
}
