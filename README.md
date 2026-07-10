# dbstore

dbstore is a small Go runtime for building repository implementations over
named backend sources.

It does not try to hide the differences between SQL, REST, messaging, object
storage, or other backends. It only standardizes the runtime boundary around a
backend client so repositories can be explicit, testable, and lifecycle-safe:

```text
Repository Interface
  -> Repository Implementation
  -> Source[T]
  -> Executor[T]
  -> Directory[T]
  -> Adapter[T]
  -> DriverBuilder[T]
```

From the infrastructure side, the same chain is assembled in reverse:

```text
DriverBuilder[T]
  -> Adapter[T]
  -> Directory[T]
  -> Executor[T]
  -> Source[T]
  -> Repository Implementation
  -> Repository Interface
```

The repository is the important application boundary. The application owns its
repository interfaces, repository implementations, and backend-specific
operations. dbstore owns source registration, lifecycle, throttling, and scoped
client access.

In other words, dbstore stops at `Source[T]`. Repository implementations keep a
source field and translate backend-specific operations into the application's
repository contract.

## Packages

```text
github.com/loykin/dbstore               core runtime
github.com/loykin/dbstore/adapters/sqlx SQL/sqlx adapter
github.com/loykin/dbstore/adapters/rest REST/HTTP adapter
github.com/loykin/dbstore/adapters/opensearch OpenSearch adapter
github.com/loykin/dbstore/adapters/elasticsearch Elasticsearch adapter
```

The root package has no SQL or REST dependency. Backend-specific helpers live
under `adapters/`.

## Core Concepts

### Driver

A driver opens one concrete client type from a `SourceConfig`.

```go
type DriverBuilder[T any] interface {
	Open(cfg dbstore.SourceConfig) (T, error)
}
```

### Adapter

An adapter registers drivers, opens named sources, and owns their lifecycle.

```go
sql := sqlxadapter.New()
sql.RegisterDefaultDrivers()
defer sql.Close()

err := sql.Open("primary", dbstore.SourceConfig{
	Driver:     sqlxadapter.DriverPostgres,
	DSN:        postgresDSN,
	PoolConfig: dbstore.DefaultPoolConfig,
})
```

### Source And Repository

A source is the runtime handle kept by repository implementations. The
repository stays application-owned; dbstore only provides scoped access to the
registered backend client.

```go
exec := sql.Executor()

type userRepo struct {
	source dbstore.Source[*sqlx.DB]
}

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB]) *userRepo {
	return &userRepo{source: dbstore.NewSource("primary", exec)}
}

func (r *userRepo) FindName(ctx context.Context, id int) (string, error) {
	var name string
	err := r.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", id).Scan(&name)
	})
	return name, err
}
```

`Executor.Run` is the lower-level primitive. Repository code should normally
use `Source.Run` or an adapter source such as `sqlx.Source` or `rest.Source`.

## SQL Adapter

Use `adapters/sqlx` when the backend client is `*sqlx.DB`.

```go
import sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
```

```go
sql := sqlxadapter.New()
sql.RegisterDefaultDrivers()
```

The application still imports the concrete `database/sql` driver package, such
as `_ "modernc.org/sqlite"` or `_ "github.com/lib/pq"`. Implement a custom
driver only when opening the client needs custom parsing, authentication, or
connection behavior beyond `sqlx.Connect(driverName, dsn)`.

Custom SQL drivers still plug into the same adapter:

```go
type TenantSQLiteDriver struct{}

func (d TenantSQLiteDriver) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
	dsn := "file:" + cfg.DSN + "?mode=memory&cache=shared"
	return sqlx.Connect(sqlxadapter.DriverSQLite, dsn)
}

sql.RegisterDriver("tenant-sqlite", TenantSQLiteDriver{})
```

Default SQL driver registrations:

```text
sqlxadapter.DriverSQLite     -> database/sql driver "sqlite"
sqlxadapter.DriverPostgres   -> database/sql driver "postgres"
sqlxadapter.DriverMySQL      -> database/sql driver "mysql"
sqlxadapter.DriverMariaDB    -> database/sql driver "mysql"
sqlxadapter.DriverClickHouse -> database/sql driver "clickhouse"
```

For repositories that need transactions, keep a `sqlxadapter.Source` field.

```go
type accountRepo struct {
	source sqlxadapter.Source
}

func NewAccountRepo(exec *dbstore.Executor[*sqlx.DB], source string) *accountRepo {
	return &accountRepo{source: sqlxadapter.NewSource(source, exec)}
}

func (r *accountRepo) Transfer(ctx context.Context, from, to int, amount int64) error {
	return r.source.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to)
		return err
	})
}
```

`sqlxadapter.RunTx` is also available when a source field is not the right fit.

## REST Adapter

Use `adapters/rest` when the backend is an HTTP/JSON API.

```go
import restadapter "github.com/loykin/dbstore/adapters/rest"
```

```go
type RESTDriver struct{}

func (d RESTDriver) Open(cfg dbstore.SourceConfig) (*restadapter.Client, error) {
	// Parse cfg.DSN and construct a backend-specific restadapter.Client.
}

rest := restadapter.New()
rest.RegisterDriver("rest", RESTDriver{})

err := rest.Open("search", dbstore.SourceConfig{
	Driver: "rest",
	DSN:    "http://localhost:9200",
})
```

Custom HTTP APIs can share this transport adapter. The repository owns paths,
request bodies, and response semantics. OpenSearch and Elasticsearch have
dedicated adapters backed by their official Go SDKs.

```go
type documentRepo struct {
	source restadapter.Source
	index string
}

func NewDocumentRepo(exec *dbstore.Executor[*restadapter.Client], source, index string) *documentRepo {
	return &documentRepo{
		source: restadapter.NewSource(source, exec),
		index:  index,
	}
}

func (r *documentRepo) Create(ctx context.Context, id, name string) error {
	return r.source.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
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

Each backend implementation keeps the source that matches its client type.
Run the same compliance suite against every implementation to catch drift.

## OpenSearch And Elasticsearch

OpenSearch and Elasticsearch use official SDK clients. The adapter package
provides the default driver and keeps the common `RegisterDriver` / `Open` /
`Executor` flow.

```go
search := opensearchadapter.New()
search.RegisterDriver("opensearch", opensearchadapter.Driver{})

err := search.Open("primary", dbstore.SourceConfig{
	Driver: "opensearch",
	DSN:    "http://localhost:9200",
})
```

Repositories use the SDK client directly:

```go
type documentRepo struct {
	source opensearchadapter.Source
}
```

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
sql.Open("meta", dbstore.SourceConfig{
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
sql.Open("tenant-"+id, cfg)
repo := NewUserRepo(exec, "tenant-"+id)

// For lower-level dynamic removal, use dbstore.Directory[T] directly.
```

## Examples

```text
examples/basic             SQLite driver registration with sqlxadapter
examples/sql_drivers       SQLite and PostgreSQL driver registration
examples/custom_sql_driver custom SQL driver registration with sqlxadapter
examples/rest              custom REST driver registration with restadapter
examples/custom_adapter    custom backend client registration with dbstore.NewAdapter[T]
examples/opensearch        OpenSearch SDK client registration
examples/elasticsearch     Elasticsearch SDK client registration
examples/repository        repository implementation with sqlxadapter.Source
examples/multi_db          multiple named SQL sources
examples/sqlite_concurrent SQLite concurrency throttling
```

## Layout

```text
dbstore.go       public core API
internal/store   core implementation
adapters/sqlx    SQL/sqlx adapter, source, transactions, pool config
adapters/rest    REST adapter, source, client helpers
adapters/opensearch OpenSearch adapter, driver, source alias
adapters/elasticsearch Elasticsearch adapter, driver, source alias
examples         runnable examples
docs             design notes
```
