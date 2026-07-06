package dbstore

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore/internal/store"
)

// Public types — thin aliases over internal/store.
type (
	Pool[T any]              = store.Pool[T]
	Executor[T any]          = store.Executor[T]
	BaseRepo[T any]          = store.BaseRepo[T]
	SQLRepo                  = store.SQLRepo
	DriverConfig             = store.DriverConfig
	PoolConfig               = store.PoolConfig
	DriverBuilder[T any]     = store.DriverBuilder[T]
	PoolConfigApplier[T any] = store.PoolConfigApplier[T]
	DriverRegistry[T any]    = store.DriverRegistry[T]
	Closer                   = store.Closer
)

// DefaultPoolConfig is a safe default connection pool configuration.
var DefaultPoolConfig = store.DefaultPoolConfig

// DefaultApplyPoolConfig applies the standard pool settings to db.
// Driver implementations can call this from their ApplyPoolConfig.
func DefaultApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	store.DefaultApplyPoolConfig(db, cfg)
}

func NewPool[T any](r *DriverRegistry[T]) *Pool[T]              { return store.NewPool[T](r) }
func NewExecutor[T any](p *Pool[T]) *Executor[T]                { return store.NewExecutor[T](p) }
func NewBaseRepo[T any](name string, e *Executor[T]) BaseRepo[T] { return store.NewBaseRepo[T](name, e) }
func NewSQLRepo(name string, e *Executor[*sqlx.DB]) SQLRepo     { return store.NewSQLRepo(name, e) }
func NewDriverRegistry[T any]() *DriverRegistry[T]              { return store.NewDriverRegistry[T]() }

// RunTx executes fn within a transaction against the *sqlx.DB registered
// under name. See store.RunTx for why this isn't an Executor[T] method.
func RunTx(e *Executor[*sqlx.DB], ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	return store.RunTx(e, ctx, name, fn)
}
