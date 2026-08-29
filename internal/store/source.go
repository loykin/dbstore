package store

import "context"

// Source provides identity-bound access to the backend client registered under
// name when the Source is constructed. Removing and reopening the same name
// invalidates this Source rather than silently retargeting it.
type Source[T any] struct {
	name  string
	exec  *Executor[T]
	entry *directoryEntry[T]
}

func NewSource[T any](name string, exec *Executor[T]) Source[T] {
	return Source[T]{name: name, exec: exec, entry: exec.directory.sourceEntry(name)}
}

func (s Source[T]) Name() string {
	return s.name
}

func (s Source[T]) Run(ctx context.Context, fn func(context.Context, T) error) error {
	return s.exec.runSource(ctx, s.name, s.entry, fn)
}
