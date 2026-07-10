package dbstore

import "github.com/loykin/dbstore/internal/store"

// Public types — thin aliases over internal/store.
type (
	Pool[T any]              = store.Pool[T]
	Executor[T any]          = store.Executor[T]
	Source[T any]            = store.Source[T]
	DriverConfig             = store.DriverConfig
	PoolConfig               = store.PoolConfig
	DriverBuilder[T any]     = store.DriverBuilder[T]
	PoolConfigApplier[T any] = store.PoolConfigApplier[T]
	DriverRegistry[T any]    = store.DriverRegistry[T]
	Closer                   = store.Closer
)

func NewPool[T any](r *DriverRegistry[T]) *Pool[T]           { return store.NewPool[T](r) }
func NewExecutor[T any](p *Pool[T]) *Executor[T]             { return store.NewExecutor[T](p) }
func NewSource[T any](name string, e *Executor[T]) Source[T] { return store.NewSource[T](name, e) }
func NewDriverRegistry[T any]() *DriverRegistry[T]           { return store.NewDriverRegistry[T]() }
