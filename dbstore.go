package dbstore

import (
	"context"

	"github.com/loykin/dbstore/internal/store"
)

// Public types — thin aliases over internal/store.
type (
	Directory[T any]         = store.Directory[T]
	Adapter[T any]           = store.Adapter[T]
	AdapterContract[T any]   = store.AdapterContract[T]
	Executor[T any]          = store.Executor[T]
	Source[T any]            = store.Source[T]
	Runner[T any]            = store.Runner[T]
	Config                   = store.Config
	SourceConfig             = store.SourceConfig
	SourceInfo               = store.SourceInfo
	PoolConfig               = store.PoolConfig
	DriverBuilder[T any]     = store.DriverBuilder[T]
	PoolConfigApplier[T any] = store.PoolConfigApplier[T]
	DriverRegistry[T any]    = store.DriverRegistry[T]
	Closer                   = store.Closer
	Observer                 = store.Observer
	MultiObserver            = store.MultiObserver
)

func NewAdapter[T any]() *Adapter[T]                         { return store.NewAdapter[T]() }
func NewSource[T any](name string, e *Executor[T]) Source[T] { return store.NewSource[T](name, e) }

func Exec[T any](ctx context.Context, src Runner[T], fn func(context.Context, T) error) error {
	return store.Exec[T](ctx, src, fn)
}

func Call[T, R any](ctx context.Context, src Runner[T], fn func(context.Context, T) (R, error)) (R, error) {
	return store.Call[T, R](ctx, src, fn)
}

var (
	DefaultPoolConfig = store.DefaultPoolConfig
	ErrNotFound       = store.ErrNotFound
)
