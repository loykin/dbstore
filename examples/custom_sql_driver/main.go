package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

// tenantSQLiteDriver shows when a custom SQL driver is useful: the application
// wants SourceConfig.DSN to be a logical tenant name, not a raw database DSN.
type tenantSQLiteDriver struct {
	prefix string
}

func (d tenantSQLiteDriver) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
	tenant := strings.TrimSpace(cfg.DSN)
	if tenant == "" {
		return nil, fmt.Errorf("tenant name is required")
	}
	if strings.ContainsAny(tenant, `/\?&=`) {
		return nil, fmt.Errorf("tenant name %q is not allowed in sqlite DSN", tenant)
	}

	dsn := fmt.Sprintf("file:%s-%s?mode=memory&cache=shared", d.prefix, tenant)
	return sqlx.Connect(sqlxadapter.DriverSQLite, dsn)
}

func (d tenantSQLiteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
}

type auditRepo struct {
	source sqlxadapter.Source
}

func newAuditRepo(exec *dbstore.Executor[*sqlx.DB], source string) *auditRepo {
	return &auditRepo{source: sqlxadapter.NewSource(source, exec)}
}

func (r *auditRepo) Init(ctx context.Context) error {
	return r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE audit_log (id INTEGER PRIMARY KEY, message TEXT NOT NULL)`)
		return err
	})
}

func (r *auditRepo) Append(ctx context.Context, message string) error {
	return r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO audit_log (message) VALUES (?)`, message)
		return err
	})
}

func (r *auditRepo) Last(ctx context.Context) (string, error) {
	var message string
	err := r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT message FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&message)
	})
	return message, err
}

func main() {
	ctx := context.Background()

	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	sql.RegisterDriver("tenant-sqlite", tenantSQLiteDriver{prefix: "audit"})
	defer sql.Close()

	if err := sql.Open("tenant-a", dbstore.SourceConfig{
		Driver: "tenant-sqlite",
		DSN:    "tenant-a",
		PoolConfig: dbstore.PoolConfig{
			MaxLifetime:    30 * time.Minute,
			MaxIdleTime:    5 * time.Minute,
			MaxConcurrency: 1,
		},
	}); err != nil {
		log.Fatal(err)
	}

	repo := newAuditRepo(sql.Executor(), "tenant-a")
	if err := repo.Init(ctx); err != nil {
		log.Fatal(err)
	}
	if err := repo.Append(ctx, "user.created"); err != nil {
		log.Fatal(err)
	}

	message, err := repo.Last(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message)
}
