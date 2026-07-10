package store

import "context"

// Source provides scoped access to a named backend client for any client
// type T. Embed it in a concrete repository or adapter struct.
type Source[T any] struct {
	name string
	exec *Executor[T]
}

func NewSource[T any](name string, exec *Executor[T]) Source[T] {
	return Source[T]{name: name, exec: exec}
}

func (s *Source[T]) Run(ctx context.Context, fn func(context.Context, T) error) error {
	return s.exec.Run(ctx, s.name, fn)
}
