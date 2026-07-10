# dbstore

dbstore is a small Go runtime for named backend sources.

It does not try to hide the differences between SQL, REST, messaging, object
storage, or other backends. It only standardizes the runtime boundary around a
backend client:

```text
DriverBuilder[T] -> Pool[T] -> Executor[T] -> Source[T]
```

The application still owns its repository interfaces and backend-specific
operations. dbstore owns source registration, lifecycle, throttling, and scoped
client access.

## Packages

```text
github.com/loykin/dbstore               core runtime
github.com/loykin/dbstore/adapters/sqlx SQL/sqlx adapter
github.com/loykin/dbstore/adapters/rest REST/HTTP adapter
```

The root package has no SQL or REST dependency. Backend-specific helpers live
under `adapters/`.

## Core Concepts

### Driver

A driver opens one concrete client type from a `DriverConfig`.

```go
type DriverBuilder[T any] interface {
	Open(cfg dbstore.DriverConfig) (T, error)
}
```

### Pool

A pool registers named sources and owns their lifecycle.

```go
registry := dbstore.NewDriverRegistry[*sqlx.DB]()
registry.Register("postgres", &PostgresDriver{})

pool := dbstore.NewPool(registry)
defer pool.RemoveAll()

err := pool.Register("primary", dbstore.DriverConfig{
	Driver:     "postgres",
	DSN:        postgresDSN,
	PoolConfig: dbstore.DefaultPoolConfig,
})
```

### Source

A source is the application-facing handle used by repository implementations.

```go
exec := dbstore.NewExecutor(pool)
source := dbstore.NewSource("primary", exec)

err := source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
	return db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", id).Scan(&name)
})
```

`Executor.Run` is the lower-level primitive. Repository code should normally
use `Source.Run` or an adapter source such as `sqlx.Source` or `rest.Source`.

## SQL Adapter

Use `adapters/sqlx` when the backend client is `*sqlx.DB`.

```go
import sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
```

```go
type PostgresDriver struct{}

func (d *PostgresDriver) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
	return sqlx.Connect("postgres", cfg.DSN)
}

func (d *PostgresDriver) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	sqlxadapter.ApplyPoolConfig(db, cfg)
}
```

For repositories that need transactions, embed `sqlxadapter.Source`.

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

`sqlxadapter.RunTx` is also available when embedding is not the right fit.

## REST Adapter

Use `adapters/rest` when the backend is an HTTP/JSON API.

```go
import restadapter "github.com/loykin/dbstore/adapters/rest"
```

```go
registry := dbstore.NewDriverRegistry[*restadapter.Client]()
registry.Register("rest", restadapter.Driver{})

pool := dbstore.NewPool(registry)
err := pool.Register("search", dbstore.DriverConfig{
	Driver: "rest",
	DSN:    "http://localhost:9200",
})
```

OpenSearch, Elasticsearch, and other REST APIs can share this transport
adapter. The repository owns paths, request bodies, and response semantics.

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

func (r *documentRepo) Create(ctx context.Context, id, name string) error {
	return r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodPut, "/"+r.index+"/_create/"+id, Document{Name: name}, nil)
	})
}
```

## Repository Contracts

dbstore does not define repository contracts. Applications do.

```go
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
}
```

Each backend implementation embeds the source that matches its client type.
Run the same compliance suite against every implementation to catch drift.

## Optional Capabilities

Drivers may implement `PoolConfigApplier[T]` when a client has tunable pool or
transport settings.

```go
type PoolConfigApplier[T any] interface {
	ApplyPoolConfig(client T, cfg dbstore.PoolConfig)
}
```

Clients may implement `Closer` when they need cleanup on `Remove` or
`RemoveAll`.

```go
type Closer interface {
	Close() error
}
```

Both are optional. Many HTTP clients implement neither.

## SQLite

SQLite should usually use one open connection and one concurrent operation to
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

## Layout

```text
dbstore.go       public core API
internal/store   core implementation
adapters/sqlx    SQL/sqlx source, transactions, pool config
adapters/rest    REST source, driver, client helpers
examples         runnable examples
docs             design notes
```
