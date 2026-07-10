package sqlxadapter

import (
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// DriverBuilder is the SQL driver contract expected by Adapter.RegisterDriver.
// SQL dialect drivers are application-owned because they choose the concrete
// database/sql driver import and connection semantics.
type DriverBuilder = dbstore.DriverBuilder[*sqlx.DB]

// ApplyPoolConfig applies dbstore.PoolConfig to a sqlx database pool.
func ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}
