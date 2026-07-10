package sqlxadapter

import (
	"fmt"

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
func ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}
