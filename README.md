# dbstore

dbstore is a small Go runtime for named backend clients: register a client
under a name, get repositories scoped and throttled access to it by that
name, and let dbstore own its lifecycle — the same way regardless of
whether the client is a `*sql.DB`, an HTTP client, or an OpenSearch SDK
client. See "Guarantees" below for exactly what that lifecycle handling
promises.

## Why

Most Go services eventually need more than one named connection to more
than one backend — a primary and a replica database, a search cluster
alongside SQL, a per-tenant database opened on demand. That usually turns
into a hand-rolled `map[string]*sql.DB` behind a mutex, a bespoke "did we
already open this one" check, an ad hoc concurrency limiter so one slow
source can't starve the rest, and a shutdown loop that closes everything
cleanly — rewritten per project and per backend type, and easy to get
subtly wrong (a lock held across a slow connect call is a classic one).
dbstore factors that out once, generically over any backend client type,
and stops there: it does not try to unify SQL transactions, REST calls, and
search queries behind one interface, since that kind of abstraction tends
to leak the moment two backends diverge in how they actually work.

**The most valuable thing this setup enables** is repository portability.
Because every backend implementation of a repository has the same
shape — a `Source[T]` embedded in the repository, `Run` scoping every
operation to the named client — swapping the backend only ever changes
`T`, never the shape of the repository. That means one behavioral test
suite, written once against the repository's interface, can run unchanged
against every implementation. This is the scenario it targets:

```go
// One contract, owned by the application:
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
}

// One suite, also owned by the application — dbstore doesn't know your
// repository's contract, so it can't write the assertions for you, only
// the loop that runs them per backend (see dbstoretest below):
func runUserRepoComplianceSuite(t *testing.T, newRepo func(t *testing.T) UserRepository) {
	t.Run("Create_and_FindByID", func(t *testing.T) {
		repo := newRepo(t)
		require.NoError(t, repo.Create(ctx, "Alice"))
		users, _ := repo.FindAll(ctx)
		u, err := repo.FindByID(ctx, users[0].ID)
		require.NoError(t, err)
		assert.Equal(t, "Alice", u.Name)
	})
	t.Run("FindByID_NotFound", func(t *testing.T) {
		_, err := newRepo(t).FindByID(ctx, 999)
		assert.Error(t, err)
	})
	// ...
}

// Run against every backend:
func TestUserRepo(t *testing.T) {
	dbstoretest.RunComplianceSuite(t, []dbstoretest.Fixture[UserRepository]{
		{Name: "SQLite", New: sqliteFixture},
		{Name: "Postgres", New: postgresFixture},
	}, runUserRepoComplianceSuite)
}
```

`sqliteFixture` and `postgresFixture` are closures that construct the same
repository type over a different `T` — a `*sqlx.DB` opened against SQLite in
one case, PostgreSQL in the other. dbstore's own tests run this same suite
against both a real SQLite database and a PostgreSQL container
(`internal/store/repo_compliance_test.go`; two separate `Test` functions
there, not `dbstoretest`, since the PostgreSQL one needs a `-tags
integration` build tag the SQLite one doesn't).

`examples/repo_compliance` goes one step further, taking `T` outside a
single backend family: the same `runUserRepoComplianceSuite` runs,
completely unchanged, against a SQLite-backed `UserRepository` and a
REST-backed one hitting a fake JSON API — `*sqlx.DB` vs
`*restadapter.Client`. Transactional rollback isn't part of that shared
suite, since not every backend can guarantee it; it's exactly the kind of
backend-specific capability that stays out of the common contract.

What makes writing that suite worth it is the layer underneath: named
registration, a per-source concurrency throttle so one slow backend can't
starve the rest, and safe concurrent open/close, the same way regardless of
whether `T` is `*sql.DB`, an HTTP client, or an OpenSearch SDK client. See
"Guarantees" below for exactly what that plumbing promises.

**Where this fits next to what you already know:**

- **Plain `database/sql` / `sqlx`** — fine for one backend, one
  implementation. Nothing to verify across implementations because there's
  only one; multi-backend lifecycle and throttling aren't in scope either.
- **An ORM (gorm, ent, ...)** — solves query building and struct mapping for
  one SQL database at a time, not cross-backend behavioral verification, and
  doesn't extend to REST or search.
- **Go Cloud Development Kit (`gocloud.dev`)** — the closest prior art: a
  `driver` interface plus `drivertest` conformance test packages, run against
  every cloud provider's implementation of `blob.Bucket`, `pubsub.Topic`,
  etc. The difference is that gocloud.dev also unifies the *operations*
  (`blob.Bucket` is one fixed API across S3/GCS/Azure), which is why it ships
  the conformance tests itself — it owns the contract. dbstore stops at
  lifecycle and leaves the contract, and the suite that verifies it, to the
  application, so it isn't limited to a fixed set of capabilities.
- **A hand-rolled registry** — works until a second backend needs to prove it
  behaves like the first, at which point either the tests diverge or someone
  builds most of `Source[T]`/`Directory[T]` anyway.

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
// under "primary".
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

	// MaxOpenConns: 1 — ":memory:" SQLite gives each connection its own
	// private database (see "SQLite" below).
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

	// Schema setup uses Executor.Run directly — Source.Run (below) is the
	// app-facing wrapper repository code normally uses instead.
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

```mermaid
flowchart TD
    A["DriverBuilder[T]<br/>RegisterDriver — opens one T from a SourceConfig"]
    B["Adapter[T]<br/>Open — registers and connects a named source"]
    C["Directory[T]<br/>lifecycle + per-source concurrency throttle"]
    D["Executor[T]<br/>scoped, throttled access to a named client"]
    E["Source[T]<br/>embedded in a repository for a Run method"]
    F[Repository Implementation]
    G[Repository Interface]

    A --> B --> C --> D --> E --> F --> G
```

The application owns repository interfaces, repository implementations, and
backend-specific operations. dbstore owns source registration, lifecycle,
throttling, and scoped client access, and stops there. Everything below —
`Config` files, transactions, REST/OpenSearch/Elasticsearch, custom
drivers — builds on this same shape.

## Guarantees

These are the concrete promises the chain above makes — verified by tests in
`internal/store` (including under `-race`), not just asserted here. Each one
is one line; expand for the precise semantics.

- **Visibility** — a source is visible only once `Open` succeeds.
- **Safe removal** — no new `Run` starts against a name after `Remove`
  returns; in-flight `Run`s finish first.
- **No double-open** — concurrent opens of the same name: exactly one wins.
- **`Configure` is not atomic** — sequential publish with best-effort
  rollback, not all-or-nothing.
- **An Observer can't crash the operation it's observing** — a panic in an
  Observer method is recovered and discarded.
- **An Observer must not call back into the same `Directory`** — that
  reenters a non-reentrant lock and panics on purpose rather than hanging.

<details>
<summary>Precise semantics</summary>

- **Visibility** — a source becomes visible to `Executor.Run`, and to
  `Open`'s duplicate-name check, only once its driver's `Open` call
  succeeds. A failed `Open` never mutates the source set.
- **Safe removal** — once `Remove` returns, no new `Run` call against that
  name will start. A `Run` already in flight when `Remove` is called is
  allowed to finish before the client is closed.
- **No double-open** — if two callers race to open the same name
  concurrently, exactly one succeeds. The other gets an error, and its own
  client is closed rather than leaked if it managed to connect one before
  losing the race.
- **`Configure` publishes sequentially and rolls back what it opened, but
  isn't atomic** — sources are opened one at a time; if any fails, every
  source *this call* already opened is closed again before the error
  returns. Sources opened earlier in the same call are genuinely visible in
  the window before that rollback — a concurrent `Run` could reach them. A
  rollback `Remove`'s own error (e.g. `Close` failing) is discarded; only
  the triggering `Open` error is returned. A name colliding with a source
  from an *earlier* `Configure` (or `Open`) call is left untouched.
- **An Observer can't crash the operation it's observing** — a panicking
  `Observer` method is recovered and discarded (see "Metrics" below); it
  cannot fail a `Run` whose `fn` succeeded or make a successful `Register`
  look like it failed.
- **An Observer must not call back into the same `Directory`** — calling
  `Register`/`Remove`/`RemoveAll`/`SetObserver` on the source's own
  `Directory` from inside an Observer method is a same-goroutine reentrancy,
  not ordinary contention, and self-deadlocks the lock that orders callback
  delivery. dbstore detects this and panics immediately instead of hanging
  forever; do that work from a separate goroutine instead.

</details>

`MaxConcurrency <= 0` means unthrottled — `Run` calls proceed without
waiting, same as if `PoolConfig` were never set. It does not block every
call, and it is not a placeholder for "apply some default".

## Packages

```text
github.com/loykin/dbstore                        core runtime
github.com/loykin/dbstore/adapters/sqlx          SQL/sqlx adapter
github.com/loykin/dbstore/adapters/rest          REST/HTTP adapter
github.com/loykin/dbstore/adapters/opensearch    OpenSearch adapter
github.com/loykin/dbstore/adapters/elasticsearch Elasticsearch adapter
github.com/loykin/dbstore/adapters/prometheus    Prometheus dbstore.Observer
github.com/loykin/dbstore/dbstoretest            compliance-suite-per-fixture test helper
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

`Configure` is not atomic in the database sense — it opens sources one at a
time and rolls back what this call opened if any fails, but sources opened
earlier in the same call are genuinely visible to concurrent `Run` calls
before that rollback happens. See "Guarantees" below for the precise
rollback scope.

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

`restadapter.Driver` covers the common case — unlike SQL dialects, `net/http`
needs no backend-specific low-level driver import, so this works for any REST
endpoint out of the box:

```go
rest := restadapter.New()
rest.RegisterDriver("rest", restadapter.Driver{})

err := rest.Open("search", dbstore.SourceConfig{
	Driver: "rest",
	DSN:    "http://localhost:9200",
})
```

Implement a custom `DriverBuilder[*restadapter.Client]` only when opening the
client needs custom auth, headers, or transport beyond what `Driver.Header`
and `Driver.HTTPClient` cover:

```go
type RESTDriver struct{}

func (d RESTDriver) Open(cfg dbstore.SourceConfig) (*restadapter.Client, error) {
	// Parse cfg.DSN and construct a backend-specific restadapter.Client.
}

rest.RegisterDriver("custom-rest", RESTDriver{})
```

**Auth** has two levels, matching whether the credential is static or must be
computed per request:

```go
// Static credentials go straight in Header — BasicAuth covers HTTP Basic Auth,
// an API key is http.Header{"X-Api-Key": []string{key}} the same way.
rest.RegisterDriver("basic-auth", restadapter.Driver{
	Header: restadapter.BasicAuth("app", "s3cret"),
})

// Auth that must be refreshed or computed per request (OAuth2 token refresh,
// request signing, mTLS) goes in HTTPClient instead — pass an *http.Client
// whose Transport is a custom http.RoundTripper. This is the same extension
// point golang.org/x/oauth2 and most cloud SDK auth helpers already target.
rest.RegisterDriver("bearer-auth", restadapter.Driver{
	HTTPClient: &http.Client{Transport: myTokenRefreshingTransport{}},
})
```

See `examples/rest` for both running against fake servers.

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

Each backend implementation keeps the source that matches its client type
(see "Why" above for what running one compliance suite against all of them
buys you). `github.com/loykin/dbstore/dbstoretest` provides
`RunComplianceSuite` and `Fixture[R]` for the "run this suite once per named
fixture" loop — it doesn't know your contract or write assertions for you,
only the loop. `examples/repo_compliance` is a full, runnable version of
this: one `UserRepository`, a SQLite-backed and a REST-backed
implementation, and one test suite run against both via
`dbstoretest.RunComplianceSuite`.

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

## Pool Size vs Throttle

For SQL backends, `MaxOpenConns` (database/sql's connection pool) and
`MaxConcurrency` (dbstore's per-source throttle) are two independent
concurrency limits stacked on top of each other. If `MaxOpenConns` is
smaller, a request that already cleared the throttle can still queue
invisibly inside database/sql waiting for a free connection — so a `ctx`
timeout no longer tells you which layer it happened in.

Keep `MaxOpenConns >= MaxConcurrency` so the throttle is always the one place
that can block, and a timeout always points there. `DefaultPoolConfig` (10
vs 5) and the SQLite example above (1 vs 1) both follow this ratio.
`sqlxadapter.ApplyPoolConfig` logs a warning when it's violated.

## Metrics

`Directory[T]`/`Executor[T]` notify an optional `dbstore.Observer` of source
lifecycle and `Run` calls — this is what actually lets you tell whether a
timeout happened waiting on the throttle or inside `fn` (see "Pool Size vs
Throttle" above), instead of just guessing.

```go
type Observer interface {
	ObserveSourceSnapshot(sources []string)
	ObserveSourceRegistered(source string)
	ObserveSourceRemoved(source string)
	ObserveAcquire(source string, waited time.Duration, err error)
	ObserveComplete(source string, duration time.Duration, err error)
}
```

`ObserveAcquire`/`ObserveComplete` bracket `fn`'s execution (acquire
succeeds → run → complete), which is what lets an Observer track in-flight
operations, not just their duration afterward. All five methods are called
synchronously — the same constraint `net/http/httptrace.ClientTrace`'s hooks
document — so an implementation must not block or do I/O.

`SetObserver` immediately calls `ObserveSourceSnapshot` with every source
already open, so attaching an Observer after `Open` doesn't leave it
thinking those sources don't exist — which would otherwise show up later as
`ObserveSourceRemoved` with no matching registration (a Prometheus
`sources_active` gauge going negative, for instance). The snapshot is a
single call, not a replayed `ObserveSourceRegistered` per source, and calling
`SetObserver` more than once — the same Observer, or a different one — always
snapshots the current set again rather than re-firing registration events:
an Observer maintaining a live count should `Set` it from `len(sources)`,
not increment per element, or repeated `SetObserver` calls inflate it (a
gauge stuck above the real count, a "registrations" counter that no longer
means "registrations"). `adapters/prometheus` does exactly this.

Core has no metrics dependency — `Observer` is vendor-neutral for the same
reason `PoolConfigApplier`/`Closer` are. `adapters/prometheus` is a
ready-made, comprehensive implementation; wiring it in is one call, and
every source registration and `Run` after that is automatically recorded:

```go
import prometheusadapter "github.com/loykin/dbstore/adapters/prometheus"

sql := sqlxadapter.New()
sql.SetObserver(prometheusadapter.New("myapp_sql", nil)) // nil -> prometheus.DefaultRegisterer
```

It exposes five metrics per namespace: `throttle_wait_seconds{source,status}`
and `run_seconds{source,status}` (histograms), `inflight{source}` and
`sources_active` (gauges), and `source_events_total{event}` (counter,
`event=registered|removed`). `status` is `ok`, `canceled` (the caller's ctx
was done — not a backend failure), or `error` (`run_seconds` only — a real
failure) — kept separate so a spike in one doesn't read as the other; folding
"my caller gave up" and "the backend broke" into one label would make that
undebuggable from the metric alone.

Calling `New` again with the same namespace and registry is safe — it
reuses the already-registered series instead of panicking, so re-running
setup code (tests, or an app that rebuilds an Adapter) doesn't need to
special-case metrics. A real name collision with an incompatible metric
still panics.

Pass an app-owned `*prometheus.Registry` instead of `nil` to share one
`/metrics` endpoint with the app's own additional metrics — dbstore's series
live alongside them in the same registry, not a separate one.

The two histograms use OpenTelemetry's recommended
`db.client.operation.duration` bucket boundaries (`0.001` to `10` seconds),
not `prometheus.DefBuckets` — `DefBuckets` is documented as tuned for network
service response times and starts at 5ms, too coarse for local/in-memory
backends where sub-millisecond calls are routine.

The same `Observer` can be shared across multiple adapters (`sqlxadapter`,
`restadapter`, ...) — it never sees the backend client type, only a source
name, durations, and an error. Apps that prefer OpenTelemetry, StatsD, or
plain logging implement `Observer` themselves instead; nothing about
`SetObserver` is Prometheus-specific. Combine more than one (e.g. Prometheus
metrics and a custom audit log) with `dbstore.MultiObserver{a, b}`, which
fans every call out to each — `SetObserver` itself only holds one. Not
calling `SetObserver` at all costs nothing — no metrics dependency is even
loaded unless `adapters/prometheus` is imported. See `examples/prometheus`
for a full run scraping `/metrics`.

## Dynamic Sources

Sources can be added and removed at runtime — e.g. opening one per tenant on
first use and tearing it down when the tenant disconnects.

```go
sql.Open("tenant-"+id, cfg)
repo := NewUserRepo(exec, "tenant-"+id)

// ...later, when the tenant is done:
err := sql.Remove("tenant-" + id)
```

`Remove` waits for that source's in-flight `Run` calls to finish and closes
its client, without touching any other source. A new `Run` against a removed
name fails immediately; the same name can be `Open`ed again afterward.

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
examples/repo_compliance   same compliance suite across SQLite and REST via dbstoretest
examples/prometheus        SetObserver wired to Prometheus metrics
```

## Layout

```text
dbstore.go             public core API
internal/store         core implementation
adapters/sqlx          SQL/sqlx adapter, source, transactions, pool config
adapters/rest          REST adapter, source, client helpers
adapters/opensearch    OpenSearch adapter, driver, source alias
adapters/elasticsearch Elasticsearch adapter, driver, source alias
adapters/prometheus    Observer implementation backed by Prometheus metrics
dbstoretest            RunComplianceSuite/Fixture[R] test helper
examples               runnable examples
```

## FAQ

**How do I run operations across two repositories in one transaction?**

dbstore doesn't provide this — `Source.Run`/`RunTx` only ever expose a client
inside their own callback, with no way to pull a `*sqlx.Tx` out and hand it
to a second repository. That's intentional: dbstore stops at lifecycle and
scoped access, and cross-repository transaction coordination is operation
semantics, which it deliberately leaves to the application (see "Why").

The fix doesn't need any new dbstore code, though — it needs repository
methods written against `sqlx.ExtContext` (satisfied by both `*sqlx.DB` and
`*sqlx.Tx`) instead of a concrete `*sqlx.DB`, so the same method works
whether it's called through `Source.Run` (normal case) or handed a
use-case-level `*sqlx.Tx` directly (cross-repository case):

```go
// The real logic takes sqlx.ExtContext — either a *sqlx.DB or a *sqlx.Tx.
func (r *userRepo) createWith(ctx context.Context, ext sqlx.ExtContext, name string) error {
	_, err := ext.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
	return err
}

// Normal path: Source.Run hands it a *sqlx.DB.
func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return r.createWith(ctx, db, name)
	})
}

// Cross-repository path: a use case gets a *sqlx.Tx from sqlxadapter.RunTx
// and passes it directly into both repositories' "with" methods, bypassing
// Source.Run for this one call so both writes share one transaction.
func (uc *signupUseCase) RegisterUser(ctx context.Context, name string) error {
	return sqlxadapter.RunTx(uc.exec, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
		if err := uc.users.createWith(ctx, tx, name); err != nil {
			return err
		}
		return uc.accounts.grantWith(ctx, tx, name, 100)
	})
}
```

This only works within one named SQL source — a `*sqlx.Tx` belongs to one
`*sqlx.DB`, so it can't span two different named sources (e.g. `"primary"`
and `"replica"`), let alone SQL and REST. That's a limit of SQL transactions
themselves, not something dbstore could paper over.
