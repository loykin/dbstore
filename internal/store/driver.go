package store

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

type DriverBuilder interface {
	Open(cfg DriverConfig) (*sqlx.DB, error)
	ApplyPoolConfig(db *sqlx.DB, cfg PoolConfig)
}

// DefaultApplyPoolConfig applies the standard pool configuration to db.
// Driver implementations can call this to reuse the default settings.
func DefaultApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}

type DriverRegistry struct {
	builders map[string]DriverBuilder
}

func NewDriverRegistry() *DriverRegistry {
	return &DriverRegistry{builders: make(map[string]DriverBuilder)}
}

func (r *DriverRegistry) Register(name string, b DriverBuilder) {
	r.builders[name] = b
}

func (r *DriverRegistry) open(cfg DriverConfig) (*sqlx.DB, error) {
	b, ok := r.builders[cfg.Driver]
	if !ok {
		return nil, fmt.Errorf("dspool: unknown driver %q", cfg.Driver)
	}
	db, err := b.Open(cfg)
	if err != nil {
		return nil, err
	}
	b.ApplyPoolConfig(db, cfg.PoolConfig)
	return db, nil
}
