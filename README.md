# dbstore

dbstore is a small Go runtime for building repository implementations over
named backend sources.

## Why

Most Go services eventually need more than one named connection to more than
one backend — a primary and a replica database, a search cluster alongside
SQL, a per-tenant database opened on demand. That usually turns into a
hand-rolled `map[string]*sql.DB` behind a mutex, a bespoke "did we already
open this one" check, an ad hoc concurrency limiter so one slow source can't
starve the rest, and a shutdown loop that closes everything cleanly. None of
that is specific to SQL or to any single backend, yet it tends to get
rewritten per project and per backend type anyway — and the REST/search
client usually doesn't get the same lifecycle discipline the database did,
because it was bolted on separately.

dbstore factors that lifecycle plumbing out once, generically over any
backend client type (`*sql.DB`, an HTTP client, an OpenSearch SDK client,
whatever `T` you have), and stops there. It deliberately does not try to
unify SQL transactions, REST calls, and search queries behind one interface —
that kind of abstraction tends to leak the moment two backends diverge in how
they actually work. Instead, registering a named source, throttling
concurrent operations against it, and closing it down safely are handled the
same way regardless of backend; what you do with the client once you have
scoped access to it stays entirely up to the repository you write.

Concretely, without dbstore this is usually where the hand-rolled version
starts:

```go
type dbRegistry struct {
	mu  sync.Mutex
	dbs map[string]*sql.DB
	sem map[string]chan struct{} // concurrency limiter, added later once one slow query starves the rest
}

func (r *dbRegistry) get(name, dsn string) (*sql.DB, error) {
	r.mu.Lock()
	defer r.mu.Unlock() // connects while holding the lock — every other source blocks until this one finishes
	if db, ok := r.dbs[name]; ok {
		return db, nil
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	r.dbs[name] = db
	r.sem[name] = make(chan struct{}, 5)
	return db, nil
}

// ...and this gets rewritten again, slightly differently, for the REST
// client and the search client, neither of which get the throttle.
```

Fixing the lock-held-during-connect bug (block registration, not lookups;
release the lock before the network call; re-check under the lock in case
another goroutine won the race; close the loser) is exactly what
`Directory[T].Register` already does — the whole registry, throttle, and
safe-shutdown code above collapses to:

```go
sql := sqlxadapter.New()
sql.RegisterDriver("postgres", myPostgresDriver{})
sql.Open("primary", dbstore.SourceConfig{
	Driver: "postgres", DSN: dsn,
	PoolConfig: dbstore.PoolConfig{MaxConcurrency: 5},
})
defer sql.Close()

exec := sql.Executor()
exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error { /* ... */ })
```

— and the identical calls, with `T` swapped, work for an HTTP client, an
OpenSearch SDK client, or anything else with the same registration,
throttling, and shutdown guarantees.

**Where this fits next to what you already know:**

- **Plain `database/sql` / `sqlx`** — fine for one connection. Multi-source
  lifecycle, per-source throttling, and non-SQL backends aren't in scope; you
  build the registry above yourself.
- **An ORM (gorm, ent, ...)** — solves query building and struct mapping, not
  source lifecycle, and is SQL-only. Adding OpenSearch or a REST API next to
  it means bolting on unrelated, differently-shaped infrastructure.
- **A hand-rolled registry** — works until it needs throttling, safe
  concurrent registration, or a second backend type, at which point it's
  usually rewritten (see above).
- **dbstore** — replaces only the registration/lifecycle/throttling/scoped-
  access layer, for any backend type, and leaves query building and
  operations to you. It composes with sqlx, an ORM's underlying `*sql.DB`, or
  a raw SDK client — whichever `T` you already have.

## Quick Start

```bash
go get github.com/loykin/dbstore
go get github.com/loykin/dbstore/adapters/sqlx
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

// userRepo is the application-owned repository. Embedding sqlxadapter.Source
// gives it scoped, throttled access to whichever *sqlx.DB is registered
// under "primary" — this is the pattern every backend implementation below
// follows, not something Quick Start simplifies away.
type userRepo struct {
	sqlxadapter.Source
}

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) *userRepo {
	return &userRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *userRepo) FindByID(ctx context.Context, id int) (string, error) {
	var name string
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, id).Scan(&name)
	})
	return name, err
}

func main() {
	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	// MaxOpenConns: 1 matters here — sqlite's ":memory:" DSN gives every new
	// connection its own private database, so a pool of more than one
	// connection would make Create's write invisible to FindByID's read.
	if err := sql.Open("primary", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		log.Fatal(err)
	}

	exec := sql.Executor()
	ctx := context.Background()

	// Schema setup is not part of the repository contract, so it stays a
	// direct Executor.Run call — the lower-level primitive Source.Run wraps.
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	users := NewUserRepo(exec, "primary")
	if err := users.Create(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}

	name, err := users.FindByID(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(name) // Alice
}
```

No external database needed — `:memory:` SQLite runs this as-is (see
`examples/basic` for the same program without the repository wrapper, and
`examples/repository` for a fuller multi-method repository).

## How It Fits Together

The Quick Start code above builds this chain, bottom-up:

```text
DriverBuilder[T]         RegisterDriver — knows how to open one T from a SourceConfig
  -> Adapter[T]           Open — registers and connects a named source
  -> Directory[T]          (lifecycle + per-source concurrency throttle)
  -> Executor[T]          Executor — scoped, throttled access to a named client
  -> Source[T]            embedded in a repository for a Run method
  -> Repository Implementation
  -> Repository Interface
```

The repository is the important application boundary. The application owns its
repository interfaces, repository implementations, and backend-specific
operations. dbstore owns source registration, lifecycle, throttling, and scoped
client access — in other words, dbstore stops at `Source[T]`. Everything
below — `Config` files, transactions, REST/OpenSearch/Elasticsearch, custom
drivers — builds on this same shape.

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

The same sources can be opened from a config-shaped struct. dbstore does not
load JSON/YAML itself; applications load into `dbstore.Config` and pass it to
the adapter.

```go
cfg := dbstore.Config{
	Sources: map[string]dbstore.SourceConfig{
		"primary": {
			Driver: sqlxadapter.DriverPostgres,
			DSN:    postgresDSN,
			PoolConfig: dbstore.PoolConfig{
				MaxOpenConns:   10,
				MaxIdleConns:   2,
				MaxConcurrency: 5,
			},
		},
	},
}

err := sql.Configure(cfg)
```

The map key is the source name — the same identifier repository code passes
to `Executor.Run` — not something meant to be renamed from config. Only the
per-source connection details are meant to vary by environment.

Equivalent JSON:

```json
{
  "sources": {
    "primary": {
      "driver": "postgres",
      "dsn": "postgres://user:pass@localhost/db",
      "pool": {
        "maxOpenConns": 10,
        "maxIdleConns": 2,
        "maxConcurrency": 5
      }
    }
  }
}
```

`Configure` validates and opens all sources atomically: if any source fails
to open, sources already opened by that call are closed again before the
error is returned.

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
examples/config            Config-driven setup spanning SQL and REST sources
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
