package sqlxadapter

import (
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// DriverBuilder is the SQL driver contract expected by Adapter.RegisterDriver.
// SQL dialect drivers are application-owned because they choose the concrete
// database/sql driver import and connection semantics.
type DriverBuilder = dbstore.DriverBuilder[*sqlx.DB]

const (
	DriverSQLite     = "sqlite"
	DriverPostgres   = "postgres"
	DriverMySQL      = "mysql"
	DriverMariaDB    = "mariadb"
	DriverClickHouse = "clickhouse"
)

// Driver opens sqlx databases using a database/sql driver name.
//
// Applications still import the concrete database/sql driver package, usually
// for side effects, for example `_ "modernc.org/sqlite"` or `_ "github.com/lib/pq"`.
type Driver struct {
	DriverName string
}

func NewDriver(driverName string) Driver {
	return Driver{DriverName: driverName}
}

func SQLiteDriver() Driver {
	return NewDriver(DriverSQLite)
}

func PostgresDriver() Driver {
	return NewDriver(DriverPostgres)
}

func MySQLDriver() Driver {
	return NewDriver(DriverMySQL)
}

func MariaDBDriver() Driver {
	return NewDriver(DriverMySQL)
}

func ClickHouseDriver() Driver {
	return NewDriver(DriverClickHouse)
}

func (d Driver) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
	if d.DriverName == "" {
		return nil, fmt.Errorf("sqlx driver name is required")
	}
	return sqlx.Connect(d.DriverName, cfg.DSN)
}

func (d Driver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	ApplyPoolConfig(db, cfg)
}

// ApplyPoolConfig applies dbstore.PoolConfig to a sqlx database pool.
//
// MaxOpenConns and MaxConcurrency are two independent concurrency limits —
// database/sql's connection pool and dbstore's per-source throttle — stacked
// on top of each other. If MaxOpenConns is smaller, requests that already
// cleared the throttle can still queue invisibly inside database/sql waiting
// for a free connection, which makes a ctx.Done() timeout ambiguous: was it
// waiting on the throttle or on the pool? Keeping MaxOpenConns comfortably at
// or above MaxConcurrency keeps the throttle as the one place that ever
// blocks, so that's always where a timeout points. See DefaultPoolConfig
// (10 vs 5) for the ratio this package assumes.
func ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	if cfg.MaxOpenConns > 0 && cfg.MaxConcurrency > 0 && cfg.MaxOpenConns < cfg.MaxConcurrency {
		log.Printf("dbstore/sqlxadapter: MaxOpenConns (%d) is less than MaxConcurrency (%d) — "+
			"requests that clear the throttle may still queue inside database/sql's pool, "+
			"making ctx timeouts ambiguous. Set MaxOpenConns >= MaxConcurrency.",
			cfg.MaxOpenConns, cfg.MaxConcurrency)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}
