package store

import (
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type sqliteDriver struct{}

func (d *sqliteDriver) Open(cfg DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d *sqliteDriver) ApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
}

func newTestRegistry() *DriverRegistry {
	r := NewDriverRegistry()
	r.Register("sqlite", &sqliteDriver{})
	return r
}

func newTestPool() *Pool {
	return NewPool(newTestRegistry())
}

func testConfig(dsn string) DriverConfig {
	return DriverConfig{
		Driver:     "sqlite",
		DSN:        dsn,
		PoolConfig: DefaultPoolConfig,
	}
}
