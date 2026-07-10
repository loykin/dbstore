package sqlxadapter

import (
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

type Adapter struct {
	core *dbstore.Adapter[*sqlx.DB]
}

func New() *Adapter {
	return &Adapter{core: dbstore.NewAdapter[*sqlx.DB]()}
}

func (a *Adapter) RegisterDriver(name string, driver dbstore.DriverBuilder[*sqlx.DB]) {
	a.core.RegisterDriver(name, driver)
}

func (a *Adapter) RegisterDefaultDrivers() {
	a.RegisterDriver(DriverSQLite, SQLiteDriver())
	a.RegisterDriver(DriverPostgres, PostgresDriver())
	a.RegisterDriver(DriverMySQL, MySQLDriver())
	a.RegisterDriver(DriverMariaDB, MariaDBDriver())
	a.RegisterDriver(DriverClickHouse, ClickHouseDriver())
}

func (a *Adapter) Open(name string, cfg dbstore.SourceConfig) error {
	return a.core.Open(name, cfg)
}

func (a *Adapter) Configure(cfg dbstore.Config) error {
	return a.core.Configure(cfg)
}

func (a *Adapter) Executor() *dbstore.Executor[*sqlx.DB] {
	return a.core.Executor()
}

func (a *Adapter) Close() {
	a.core.Close()
}
