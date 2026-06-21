package dbstore

import (
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore/internal/store"
)

// Public types — thin aliases over internal/store.
type (
	Pool           = store.Pool
	Executor       = store.Executor
	BaseRepo       = store.BaseRepo
	DriverConfig   = store.DriverConfig
	PoolConfig     = store.PoolConfig
	DriverBuilder  = store.DriverBuilder
	DriverRegistry = store.DriverRegistry
)

// DefaultPoolConfig is a safe default connection pool configuration.
var DefaultPoolConfig = store.DefaultPoolConfig

// DefaultApplyPoolConfig applies the standard pool settings to db.
// Driver implementations can call this from their ApplyPoolConfig.
func DefaultApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	store.DefaultApplyPoolConfig(db, cfg)
}

func NewPool(r *DriverRegistry) *Pool              { return store.NewPool(r) }
func NewExecutor(p *Pool) *Executor                { return store.NewExecutor(p) }
func NewBaseRepo(name string, e *Executor) BaseRepo { return store.NewBaseRepo(name, e) }
func NewDriverRegistry() *DriverRegistry           { return store.NewDriverRegistry() }
