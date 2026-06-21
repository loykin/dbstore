# dbstore

A Go library for registering and managing multiple databases by name.

Provides connection pool management, Repository pattern, and transactions through a single interface.

## Why

Go's `database/sql` has a built-in connection pool, but with default settings idle connections are never reclaimed. Without `SetConnMaxIdleTime`, idle connections pile up, hit `MaxOpenConns`, and new queries fail to acquire a connection.

dbstore solves the following problems:

- **Idle connection leak** — enforces a `MaxIdleTime` default to automatically reclaim idle connections
- **Multi-DB management** — registers and looks up Primary/Replica, analytics DB, service DB, etc. by name
- **Concurrent query surge protection** — per-datasource semaphore controls query bursts against a specific DB
- **Connection lifecycle** — the library manages connections directly; callers never call `db.Close()`
- **Repository pattern** — embed `BaseRepo` to build an interface-based data layer
- **Transactions** — `RunTx` handles commit/rollback automatically

## Installation

```bash
go get github.com/loykin/dbstore
```

## Architecture

```
dbstore (library)            app
─────────────────────        ──────────────────────────────
Pool                    →    pool.Register("primary", cfg)
Executor                →    executor.Run / executor.RunTx
BaseRepo (embed)        →    type userRepo struct { dbstore.BaseRepo }
                             type UserRepository interface { ... }
                             func NewUserRepo(...) UserRepository
```

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

### 2. Initialize the pool

```go
registry := dbstore.NewDriverRegistry()
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

```go
err := executor.RunTx(ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
    if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
        return err
    }
    _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to)
    return err
    // rolls back automatically on error, commits on success
})
```

### 5. Repository pattern

```go
// app defines the interface
type UserRepository interface {
    Create(ctx context.Context, name string) error
    FindByID(ctx context.Context, id int) (*User, error)
    Delete(ctx context.Context, id int) error
}

// app writes the implementation — embed BaseRepo
type userRepo struct {
    dbstore.BaseRepo
}

// compile-time contract check
var _ UserRepository = (*userRepo)(nil)

func NewUserRepo(exec *dbstore.Executor, name string) UserRepository {
    return &userRepo{dbstore.NewBaseRepo(name, exec)}
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
driver.go    — DriverBuilder interface, DriverRegistry, DefaultApplyPoolConfig
throttle.go  — per-datasource semaphore
entry.go     — poolEntry (internal)
pool.go      — Pool (Register / Remove / RemoveAll)
executor.go  — Executor (Run / RunTx)
repo.go      — BaseRepo (Run / RunTx)
```
