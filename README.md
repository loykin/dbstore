# dbstore

dbstore is a Go runtime for named backend sources.

It opens backend clients by name, manages their lifecycle, applies per-source
concurrency limits, and gives repository implementations one consistent way to
access the selected client.

The backend client can be a SQL database, an OpenSearch client, a NATS
connection, Redis, S3, a gRPC client, or any other type:

```go
type T = *sqlx.DB
type T = *restadapter.Client
type T = *nats.Conn
```

dbstore does not try to make those backends behave the same. It only
standardizes the runtime boundary around them.

## Core Model

```text
DriverBuilder[T] -> Pool[T] -> Executor[T] -> Source[T] -> app repository
```

The lower side is a backend plugin shape:

```go
type DriverBuilder[T any] interface {
	Open(cfg dbstore.DriverConfig) (T, error)
}
```

The upper side is a source adapter shape:

```go
type userRepo struct {
	dbstore.Source[*SomeClient]
}
```

`Pool[T]`, `Executor[T]`, and `Source[T]` do not know whether `T` is SQL, REST,
messaging, object storage, or something else. They only manage:

- named source registration
- open/remove/remove-all lifecycle
- per-source concurrency throttling
- scoped `Run` access
- optional close
- optional pool configuration

Backend-specific operations stay backend-specific. SQL transactions,
OpenSearch indexing/search, NATS publish/subscribe, and object storage uploads
are not forced into one common operation interface.

## Why

Applications with multiple backend implementations of the same repository
interface usually repeat two kinds of plumbing:

1. Source lifecycle plumbing gets rewritten for each backend config.
2. Backend implementations drift when the same repository contract is
   implemented by more than one backend.

dbstore addresses the first problem with reusable runtime primitives:
`Pool[T]`, `Executor[T]`, and `Source[T]`.

It helps with the second problem by making it easy to run one compliance test
suite against every implementation of an application repository interface.

## Installation

```bash
go get github.com/loykin/dbstore
```

## Source Lifecycle

Implement a driver for the concrete client type.

```go
type PostgresDriver struct{}

func (d *PostgresDriver) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("postgres", cfg.DSN)
}

func (d *PostgresDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	sqlxadapter.ApplyPoolConfig(db, cfg)
}
```

Register the driver, then register one or more named sources.

```go
registry := dbstore.NewDriverRegistry[*sqlx.DB]()
registry.Register("postgres", &PostgresDriver{})

pool := dbstore.NewPool(registry)
defer pool.RemoveAll()

err := pool.Register("primary", dbstore.DriverConfig{
	Driver:     "postgres",
	DSN:        "host=localhost user=app dbname=mydb sslmode=disable",
	PoolConfig: dbstore.DefaultPoolConfig,
})
```

Run work against a named source.

```go
executor := dbstore.NewExecutor(pool)

err := executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
	return db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", id).Scan(&name)
})
```

## Repository Source

Applications define their own repository interface. dbstore only supplies the
source adapter used by the implementation.

```go
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
}

type userRepo struct {
	dbstore.Source[*sqlx.DB]
}

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) UserRepository {
	return &userRepo{Source: dbstore.NewSource(source, exec)}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES ($1)`, name)
		return err
	})
}
```

The same shape works for REST clients. OpenSearch and Elasticsearch can both be
implemented as repository logic over `restadapter.Client`; the adapter owns HTTP
transport, while the repository owns paths and payloads.

```go
type documentRepo struct {
	restadapter.Source
	index string
}

func NewDocumentRepo(exec *dbstore.Executor[*restadapter.Client], source, index string) *documentRepo {
	return &documentRepo{
		Source: restadapter.NewSource(source, exec),
		index:  index,
	}
}
```

## SQL Capability

SQL transactions are a capability on top of `Source[*sqlx.DB]`, not part of the
generic source contract.

Use `sqlxadapter.Source` when a SQL repository needs `RunTx`.

```go
import sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
```

```go
type accountRepo struct {
	sqlxadapter.Source
}

func NewAccountRepo(exec *dbstore.Executor[*sqlx.DB], source string) *accountRepo {
	return &accountRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *accountRepo) Transfer(ctx context.Context, from, to int, amount int64) error {
	return r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to)
		return err
	})
}
```

`RunTx` also exists as a package-level helper in the SQL adapter package.

```go
err := sqlxadapter.RunTx(executor, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expired_at < now()`)
	return err
})
```

There is no generic transaction API for `Executor[T]` because most backend
clients do not have SQL transaction semantics. The root `dbstore` package does
not import `sqlx`; SQL behavior lives in `sqlxadapter`.

## REST Capability

REST is a transport adapter, not an OpenSearch-specific adapter.

```go
registry := dbstore.NewDriverRegistry[*restadapter.Client]()
registry.Register("rest", restadapter.Driver{})

pool := dbstore.NewPool(registry)
pool.Register("search", dbstore.DriverConfig{
	Driver: "rest",
	DSN:    "http://localhost:9200",
})
```

The repository decides whether that REST source speaks OpenSearch,
Elasticsearch, or another HTTP API.

```go
func (r *documentRepo) Create(ctx context.Context, id, name string) error {
	return r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodPut, "/"+r.index+"/_create/"+id, Document{Name: name}, nil)
	})
}
```

## Other Plugin Shapes

A non-REST backend uses the same runtime shape. For example, a NATS driver would
open a `*nats.Conn`, and a repository would embed `dbstore.Source[*nats.Conn]`.

## Optional Capabilities

Drivers may implement `PoolConfigApplier[T]` if their client has tunable pool
settings.

```go
type PoolConfigApplier[T any] interface {
	ApplyPoolConfig(client T, cfg dbstore.PoolConfig)
}
```

Clients may implement `Closer` if they need cleanup on `Remove` or
`RemoveAll`.

```go
type Closer interface {
	Close() error
}
```

Both are optional. HTTP-style clients often implement neither.

## SQL Pool Defaults

`DefaultPoolConfig` is useful for `database/sql` clients through `sqlx`. Apply
it with `sqlxadapter.ApplyPoolConfig` from a SQL driver.

```go
var DefaultPoolConfig = PoolConfig{
	MaxOpenConns:   10,
	MaxIdleConns:   2,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 5,
}
```

SQLite should normally use one open connection and one concurrent operation to
avoid write lock contention.

```go
pool.Register("meta", dbstore.DriverConfig{
	Driver: "sqlite",
	DSN:    "./meta.db",
	PoolConfig: dbstore.PoolConfig{
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		MaxConcurrency: 1,
	},
})
```

## Dynamic Sources

Sources can be added and removed at runtime.

```go
pool.Register("tenant-"+id, cfg)
repo := NewUserRepo(exec, "tenant-"+id)

pool.Remove("tenant-"+id)
```

## Testing Multiple Backends

dbstore does not make two backend implementations equivalent. The application
repository interface and its compliance tests do that.

```go
func RunUserRepositorySuite(t *testing.T, setup func(*testing.T) UserRepository) {
	repo := setup(t)
	// Create -> FindByID -> Delete assertions go here.
}
```

Run the same suite against SQLite, Postgres, OpenSearch, or any other backend
that claims to implement the same application repository contract.

## File Structure

```text
config.go    - DriverConfig, PoolConfig, DefaultPoolConfig
driver.go    - DriverBuilder[T], PoolConfigApplier[T], DriverRegistry[T]
throttle.go  - per-source semaphore
entry.go     - poolEntry[T]
pool.go      - Pool[T], registration, removal, optional close
executor.go  - Executor[T], Run
repo.go      - Source[T]
adapters/sqlx - sqlx Source, RunTx, ApplyPoolConfig
adapters/rest - REST Source, Driver, Client
```
