package store

import (
	"context"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type sqliteDriver struct{}

func (d *sqliteDriver) Open(cfg SourceConfig) (*sqlx.DB, error) {
	return sqlx.Connect("sqlite", cfg.DSN)
}

func (d *sqliteDriver) ApplyPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	applySQLPoolConfig(db, cfg)
}

func applySQLPoolConfig(db *sqlx.DB, cfg PoolConfig) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
}

func newTestRegistry() *DriverRegistry[*sqlx.DB] {
	r := NewDriverRegistry[*sqlx.DB]()
	r.Register("sqlite", &sqliteDriver{})
	return r
}

func newTestDirectory() *Directory[*sqlx.DB] {
	return NewDirectory(newTestRegistry())
}

func testConfig(dsn string) SourceConfig {
	return SourceConfig{
		Driver:     "sqlite",
		DSN:        dsn,
		PoolConfig: DefaultPoolConfig,
	}
}

func runSQLTx(exec *Executor[*sqlx.DB], ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	return exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		if err := fn(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}
