package dbstore

import "github.com/loykin/dbstore/internal/store"

// Public types — thin aliases over internal/store.
type (
	Directory[T any]         = store.Directory[T]
	Adapter[T any]           = store.Adapter[T]
	AdapterContract[T any]   = store.AdapterContract[T]
	Executor[T any]          = store.Executor[T]
	Source[T any]            = store.Source[T]
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

var DefaultPoolConfig = store.DefaultPoolConfig
