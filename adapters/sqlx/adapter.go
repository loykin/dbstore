package sqlxadapter

import (
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

type Adapter struct {
	core *dbstore.Adapter[*sqlx.DB]
}

type Option = dbstore.AdapterOption

func WithObserver(observer dbstore.Observer) Option {
	return dbstore.WithObserver(observer)
}

var _ dbstore.AdapterContract[*sqlx.DB] = (*Adapter)(nil)

func New(options ...Option) *Adapter {
	return &Adapter{core: dbstore.NewAdapter[*sqlx.DB](options...)}
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

func (a *Adapter) Remove(name string) error {
	return a.core.Remove(name)
}

func (a *Adapter) Sources() []dbstore.SourceInfo {
	return a.core.Sources()
}

func (a *Adapter) Executor() *dbstore.Executor[*sqlx.DB] {
	return a.core.Executor()
}

// Source returns a Runner[Handle] scoped to name, combining Executor() and
// NewSource into the one call a domain repository actually needs.
// Not part of AdapterContract[T]: its return type (Runner[Handle]) isn't
// expressible generically in terms of T, since Handle is this package's
// own type, not the core adapter's raw client type.
func (a *Adapter) Source(name string) Source {
	return NewSource(name, a.core.Executor())
}

func (a *Adapter) Close() {
	a.core.Close()
}
