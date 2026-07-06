package store

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// DriverBuilder opens a new client of type T from cfg.
type DriverBuilder[T any] interface {
	Open(cfg DriverConfig) (T, error)
}

// PoolConfigApplier is an optional capability a DriverBuilder can implement
// to tune pool settings right after Open. It is kept separate from
// DriverBuilder (not a required method) because most PoolConfig fields
// (MaxOpenConns, MaxIdleConns, ...) are specific to database/sql connection
// pools and have no equivalent for non-SQL clients.
type PoolConfigApplier[T any] interface {
	ApplyPoolConfig(client T, cfg PoolConfig)
}

// DefaultApplyPoolConfig applies the standard pool configuration to db.
// Driver implementations can call this to reuse the default settings.
func DefaultApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}

type DriverRegistry[T any] struct {
	builders map[string]DriverBuilder[T]
}

func NewDriverRegistry[T any]() *DriverRegistry[T] {
	return &DriverRegistry[T]{builders: make(map[string]DriverBuilder[T])}
}

func (r *DriverRegistry[T]) Register(name string, b DriverBuilder[T]) {
	r.builders[name] = b
}

func (r *DriverRegistry[T]) open(cfg DriverConfig) (T, error) {
	var zero T
	b, ok := r.builders[cfg.Driver]
	if !ok {
		return zero, fmt.Errorf("dspool: unknown driver %q", cfg.Driver)
	}
	client, err := b.Open(cfg)
	if err != nil {
		return zero, err
	}
	// Asserted on the builder b, not the freshly-opened client: it's the
	// builder (author of Open) that optionally knows how to tune the client
	// it just produced, mirroring closeClient's assertion on the client
	// itself for the opposite (teardown) capability in pool.go.
	if applier, ok := any(b).(PoolConfigApplier[T]); ok {
		applier.ApplyPoolConfig(client, cfg.PoolConfig)
	}
	return client, nil
}
