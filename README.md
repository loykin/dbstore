# dbstore

A Go library for registering and managing multiple databases — and, more generally, multiple backend implementations of the same repository interface — by name.

Provides connection pool management, Repository pattern, and transactions through a single interface.

## Why

The problem dbstore actually solves isn't "support more drivers." Any app with more than one datasource (primary/replica, per-tenant DBs, or a SQL backend alongside a non-SQL one) runs into two recurring costs:

1. **Connection/lifecycle/throttle plumbing gets rewritten per datasource config.** `Register`/`Remove`/`RemoveAll` + a per-datasource concurrency throttle should be written once and reused, not reimplemented for every new backend.
2. **Backend implementations drift.** When the same repository interface has two implementations (e.g. Postgres and SQLite, or a SQL backend and a search/document backend), a fix applied to one is easy to forget in the other — and that gap is hard to catch by reading code alone.

dbstore addresses (1) with generic `Pool[T]`/`Executor[T]`/`BaseRepo[T]`: the lifecycle machinery is written once and reused regardless of client type. It addresses (2) by making it easy to run one compliance test suite against every backend implementation of an interface, so drift surfaces as a failing test instead of a production bug.

On top of that, dbstore also solves the everyday `database/sql` pain points:

- **Idle connection leak** — enforces a `MaxIdleTime` default to automatically reclaim idle connections (Go's `database/sql` pool never reclaims them on its own)
- **Multi-DB management** — registers and looks up Primary/Replica, analytics DB, service DB, etc. by name
- **Concurrent query surge protection** — per-datasource semaphore controls query bursts against a specific DB
- **Connection lifecycle** — the library manages connections directly; callers never call `db.Close()`
- **Repository pattern** — embed `BaseRepo`/`SQLRepo` to build an interface-based data layer
- **Transactions** — `RunTx` handles commit/rollback automatically

## Installation

```bash
go get github.com/loykin/dbstore
```

## Architecture

```
dbstore (library)            app
─────────────────────        ──────────────────────────────
Pool[T]                 →    pool.Register("primary", cfg)
Executor[T]             →    executor.Run / dbstore.RunTx(executor, ...)
BaseRepo[T] (embed)     →    type userRepo struct { dbstore.SQLRepo }
                             type UserRepository interface { ... }
                             func NewUserRepo(...) UserRepository
```

`Pool`/`Executor`/`BaseRepo` are generic over the client type `T` (e.g. `*sqlx.DB`); everything below uses `T = *sqlx.DB`, the common case.

dbstore provides only the infrastructure. Interfaces and implementations are defined by the application.

## Quick Start

### 1. Implement a driver

```go
type PostgresDriver struct{}

func (d *PostgresDriver) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
    return sqlx.Connect("postgres", cfg.DSN)
}

func (d *PostgresDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
    dbstore.DefaultApplyPoolConfig(db, cfg)
}
```

`ApplyPoolConfig` is optional — implement it only if your client type has connection-pool settings to tune (see `dbstore.PoolConfigApplier[T]`). A driver for a non-SQL client (no connection pool to configure) can skip it entirely.

### 2. Initialize the pool

```go
registry := dbstore.NewDriverRegistry[*sqlx.DB]()
registry.Register("postgres", &PostgresDriver{})

pool := dbstore.NewPool(registry)
defer pool.RemoveAll()

pool.Register("primary", dbstore.DriverConfig{
    Driver:     "postgres",
    DSN:        "host=localhost user=app dbname=mydb sslmode=disable",
    PoolConfig: dbstore.DefaultPoolConfig,
})

pool.Register("analytics", dbstore.DriverConfig{
    Driver: "clickhouse",
    DSN:    "clickhouse://localhost:9000/stats",
    PoolConfig: dbstore.PoolConfig{
        MaxOpenConns:   20,
        MaxIdleConns:   4,
        MaxLifetime:    time.Hour,
        MaxIdleTime:    10 * time.Minute,
        MaxConcurrency: 10,
    },
})
```

### 3. Run a query directly

```go
executor := dbstore.NewExecutor(pool)

err := executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
    return db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", id).Scan(&name)
})
```

### 4. Transactions

`RunTx` is a package-level function, not an `Executor` method — transactions are a `*sqlx.DB`-specific concept that doesn't generalize to every client type `T`, so it's constrained to `*Executor[*sqlx.DB]` instead of being part of the generic `Executor[T]` API.

```go
err := dbstore.RunTx(executor, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
    if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
        return err
    }
    _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to)
    return err
    // rolls back automatically on error, commits on success
})
```

### 5. Repository pattern

See `examples/repository` for a runnable version of this pattern.

```go
// app defines the interface
type UserRepository interface {
    Create(ctx context.Context, name string) error
    FindByID(ctx context.Context, id int) (*User, error)
    Delete(ctx context.Context, id int) error
}

// app writes the implementation — embed SQLRepo for RunTx support
// (embed dbstore.BaseRepo[*sqlx.DB] directly instead if the repo only
// ever needs Run, not transactions).
type userRepo struct {
    dbstore.SQLRepo
}

// compile-time contract check
var _ UserRepository = (*userRepo)(nil)

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], name string) UserRepository {
    return &userRepo{dbstore.NewSQLRepo(name, exec)}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
    return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
        _, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES ($1)`, name)
        return err
    })
}

func (r *userRepo) FindByID(ctx context.Context, id int) (*User, error) {
    var u User
    err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
        return db.GetContext(ctx, &u, `SELECT * FROM users WHERE id = $1`, id)
    })
    return &u, err
}

func (r *userRepo) Delete(ctx context.Context, id int) error {
    return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
        _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
        return err
    })
}
```

For a non-SQL REST client example, see `examples/opensearch`. It uses
`BaseRepo[*opensearchapi.Client]` directly because OpenSearch has no SQL
transaction concept.

### 6. Wiring (main.go)

```go
pool := dbstore.NewPool(registry)
pool.Register("primary", cfg)
defer pool.RemoveAll()

exec := dbstore.NewExecutor(pool)
userRepo := NewUserRepo(exec, "primary")

svc := NewUserService(userRepo)
```

## SQLite

SQLite does not support concurrent writes, so set `MaxOpenConns: 1` and `MaxConcurrency: 1`. Parallel queries queue up and avoid `database is locked` errors.

```go
func (d *SQLiteDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
    db.SetMaxOpenConns(1)
    db.SetMaxIdleConns(1)
    db.SetConnMaxIdleTime(cfg.MaxIdleTime)
    db.SetConnMaxLifetime(cfg.MaxLifetime)
}

pool.Register("meta", dbstore.DriverConfig{
    Driver: "sqlite",
    DSN:    "./meta.db",
    PoolConfig: dbstore.PoolConfig{MaxConcurrency: 1},
})
```

## Read/Write Split

```go
pool.Register("write", dbstore.DriverConfig{Driver: "postgres", DSN: primaryDSN})
pool.Register("read",  dbstore.DriverConfig{Driver: "postgres", DSN: replicaDSN})

executor.Run(ctx, "write", func(ctx context.Context, db *sqlx.DB) error { ... })
executor.Run(ctx, "read",  func(ctx context.Context, db *sqlx.DB) error { ... })
```

## Dynamic Registration

Databases can be added and removed at runtime. Suitable for Grafana-style multi-tenant setups.

```go
// add a tenant DB at runtime
pool.Register("tenant-"+id, cfg)
repo := NewUserRepo(exec, "tenant-"+id)

// remove at runtime
pool.Remove("tenant-"+id)
```

## Testing

Swap to an in-memory SQLite by changing only the registered datasource.

```go
// test
pool.Register("primary", dbstore.DriverConfig{Driver: "sqlite", DSN: ":memory:"})
repo := NewUserRepo(dbstore.NewExecutor(pool), "primary")

// production — no code changes
pool.Register("primary", dbstore.DriverConfig{Driver: "postgres", DSN: postgresDSN})
repo := NewUserRepo(dbstore.NewExecutor(pool), "primary")
```

## DefaultPoolConfig

```go
var DefaultPoolConfig = PoolConfig{
    MaxOpenConns:   10,
    MaxIdleConns:   2,               // 20% of MaxOpen — the rest are returned to the OS
    MaxLifetime:    30 * time.Minute,
    MaxIdleTime:    5 * time.Minute, // automatically reclaims idle connections
    MaxConcurrency: 5,               // per-datasource concurrent query cap
}
```

## File Structure

```
config.go    — DriverConfig, PoolConfig, DefaultPoolConfig
driver.go    — DriverBuilder[T], PoolConfigApplier[T] (optional), DriverRegistry[T], DefaultApplyPoolConfig
throttle.go  — per-datasource semaphore
entry.go     — poolEntry[T] (internal)
pool.go      — Pool[T] (Register / Remove / RemoveAll), Closer (optional)
executor.go  — Executor[T] (Run), RunTx (package-level, *sqlx.DB only)
repo.go      — BaseRepo[T] (Run), SQLRepo (BaseRepo[*sqlx.DB] + RunTx)
```

`Closer` and `PoolConfigApplier[T]` are optional capabilities, checked via type assertion — a client type only needs to implement them if it actually has something to close or pool-configure. Non-SQL clients (e.g. an HTTP-based client) typically implement neither.
