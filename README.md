# dbstore

Every project that runs more than one named backend client reinvents the same
failure modes: opening the same name twice, running work against a name
mid-removal, one slow source starving the rest, a shutdown that leaks a
client — usually solved once per project, per backend type, and easy to get
subtly wrong (a lock held across a slow connect call is a classic one).
dbstore solves that once, generically over any client type (`*sql.DB`, an
HTTP client, an OpenSearch SDK client, ...).

When one repository contract needs more than one backend, dbstore also
generates the delegation boilerplate so one behavioral test suite verifies
every implementation instead of two independently-drifting ones. **In
numbers:** a single-backend repository needs none of this generator
machinery — 4 lines of setup, see "Single-Backend Quick Start" below. A
4-method repository shared across SQLite and REST
(`examples/repo_compliance`) is ~285 lines total, and about 155 of those are
tests you'd want regardless of dbstore; the rest is a 10-line YAML config
and two ~50-line backend bodies. The full breakdown is under "Why".

See "Guarantees" below for exactly what dbstore's lifecycle handling
promises.

## Start Here: What You Actually Build

Choose the path based on the problem you have:

| Situation | Recommended path |
|---|---|
| One repository, one backend | Write the repository directly; see "Single-Backend Quick Start" |
| One repository contract implemented by two or more backends | Use `dbstore-gen`; follow the workflow below |
| Only named client lifecycle/throttling is needed | Use `Adapter` and `Executor`; no repository generator is required |

The generator path is the main repository-portability workflow. You write the
domain decisions; dbstore writes the delegation and construction boilerplate.

### 1. Write the domain interface

```go
// user_repo.go
package users

import "context"

//go:generate go tool dbstore-gen -config user_repo.gen.yaml

type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
}
```

### 2. List the implementations to generate

```yaml
# user_repo.gen.yaml
interface: UserRepository
source: user_repo.go
test: true
backends:
  - name: sqlite
    adapter: sqlite
  - name: rest
    adapter: rest
```

Install and run the generator:

```bash
go get -tool github.com/loykin/dbstore/cmd/dbstore-gen@latest
go generate ./...
```

The resulting files have explicit ownership:

| File | Owner | What to do |
|---|---|---|
| `user_repo.go` | You | Define domain types and `UserRepository` |
| `user_repo.gen.yaml` | You | Declare the interface and backend list |
| `user_repo_gen.go` | Generator | Do not edit; delegation and type checks |
| `user_repo_sqlite.go` | You after first generation | Fill the generated `TODO` method bodies |
| `user_repo_rest.go` | You after first generation | Fill the generated `TODO` method bodies |
| `user_repo_compliance_test.go` | You after first generation | Write shared behavioral assertions and capability rules |
| `user_repo_sqlite_test.go` | You after first generation | Build the SQLite fixture and set its capabilities |
| `user_repo_rest_test.go` | You after first generation | Build the REST fixture and set its capabilities |
| `user_repo_compliance_gen_test.go` | Generator | Do not edit; registers every configured backend fixture |

Generated glue and the fixture registry are overwritten safely. Backend,
compliance-suite, and fixture skeletons are created only once, so later
generation never overwrites your implementations or assertions. Adding a
backend to the YAML regenerates the registry and creates its missing fixture
stub, which prevents a configured backend from being silently omitted from the
shared suite.

**What this actually costs**, measured on `examples/repo_compliance` (4
methods, SQLite + REST): ~285 lines across every file in the table above
marked "You" — and roughly 155 of those are the compliance suite and two
fixtures, tests you'd write by hand anyway to trust two independent
implementations behave the same. The generator's own tax is the 10-line YAML
plus the two ~50-line backend bodies; the two "Generator"-owned rows
(~87 lines) you never write or touch at all.

### 3. Fill only the backend behavior

The generated backend file already contains the type, method signatures, and
application-facing constructor. Replace the `panic` bodies with backend logic:

```go
type SqliteUserBackend struct{}

func NewSqliteUserRepository(source sqlxadapter.Source) UserRepository {
	return NewUserRepo[sqlxadapter.Handle](SqliteUserBackend{}, source)
}

func (SqliteUserBackend) FindByID(
	ctx context.Context,
	h sqlxadapter.Handle,
	id int,
) (*User, error) {
	var user User
	err := h.Get(ctx, &user, `SELECT id, name FROM users WHERE id = ?`, id)
	return &user, err
}
```

The generic call inside `NewSqliteUserRepository` is generated boilerplate.
Application setup does not use `Runner`, `Handle` type arguments, or generated
wrapper types.

### 4. Construct and use the repository

```go
sql := sqlxadapter.New()
sql.RegisterDefaultDrivers()
defer sql.Close()

if err := sql.Open("primary", cfg); err != nil {
	return err
}

users := NewSqliteUserRepository(sql.Source("primary"))
user, err := users.FindByID(ctx, 1)
```

At this boundary the application sees only its `UserRepository`, the generated
backend constructor, and the named source. See
[`docs/repository-pattern.md`](docs/repository-pattern.md) for transactions,
not-found behavior, and the shared compliance-suite pattern.

## Why

The failure modes on the first page are the "solves it once" half of
dbstore's value. It stops there deliberately: it does not try to unify SQL
transactions, REST calls, and search queries behind one interface, since
that kind of abstraction tends to leak the moment two backends diverge in
how they actually work.

**The most valuable thing this setup enables** is repository portability.
Every backend implementation of a repository has the same shape: a
`Source` field — **never embedded**, always named, so its `Run` method
can't get promoted onto the repository and leak infra access past the
repository's own interface — handing a backend-specific `Handle` (never
the raw client) into a callback. Swapping the backend only ever changes
which `Handle` type is in play, never the shape of the repository. That
means one behavioral test suite, written once against the repository's
interface, can run unchanged against every implementation. This is the
scenario it targets:

```go
// One contract, owned by the application:
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
}

type userRepoCapabilities struct {
	AtomicBatch bool
}

// One suite, also owned by the application — dbstore doesn't know your
// repository's contract, so it can't write the assertions for you, only
// the loop that runs them per backend (see dbstoretest below). caps gates
// assertions not every backend can honor (e.g. transactional atomicity).
func runUserRepoComplianceSuite(t *testing.T, newRepo func(t *testing.T) UserRepository, caps userRepoCapabilities) {
	t.Run("Create_and_FindByID", func(t *testing.T) {
		repo := newRepo(t)
		require.NoError(t, repo.Create(ctx, "Alice"))
		users, _ := repo.FindAll(ctx)
		u, err := repo.FindByID(ctx, users[0].ID)
		require.NoError(t, err)
		assert.Equal(t, "Alice", u.Name)
	})
	t.Run("FindByID_NotFound", func(t *testing.T) {
		u, err := newRepo(t).FindByID(ctx, 999)
		require.ErrorIs(t, err, dbstore.ErrNotFound)
		assert.Nil(t, u)
	})
	// ...
}

// Each user-owned backend fixture declares construction and capabilities:
var sqliteUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "SQLite", New: sqliteFixture,
	Caps: userRepoCapabilities{AtomicBatch: true},
}

// With test: true, dbstore-gen writes TestUserRepoCompliance in
// user_repo_compliance_gen_test.go and registers every configured fixture.
```

`sqliteFixture` and `postgresFixture` are closures that construct the same
repository type over a different backend `Handle` — `sqlxadapter.Handle`
in both cases here, since dialect differences are absorbed inside the
adapter (see "SQL Adapter" below). dbstore's own tests run this same suite
against both a real SQLite database and a PostgreSQL container
(`internal/store/repo_compliance_test.go`; two separate `Test` functions
there, not `dbstoretest`, since the PostgreSQL one needs a `-tags
integration` build tag the SQLite one doesn't).

`examples/repo_compliance` goes one step further, taking the backend
outside a single family: the same `runUserRepoComplianceSuite` runs,
completely unchanged, against a SQLite-backed `UserRepository` and a
REST-backed one hitting a fake JSON API — `sqlxadapter.Handle` vs
`restadapter.Handle`. Transactional rollback isn't asserted for the REST
fixture, since REST has no transaction concept; its `Fixture.Caps` simply
leaves `AtomicBatch` false and the suite skips that one assertion for it —
see "Code Generator And Capabilities" below.

**In numbers**, `examples/repo_compliance` end to end — a 4-method
`UserRepository` over SQLite and REST:

| You write | Lines |
|---|---|
| Domain interface (`user_repo.go`) + generator YAML | 32 |
| Two backend implementations (SQLite + REST, `Handle`-based) | 98 |
| Compliance suite (`runUserRepoComplianceSuite`) + two fixtures | 155 |
| **Total application-owned** | **285** |
| Generated wrapper + fixture registry — never touched | 87 |

155 of the 285 lines are the compliance suite and fixtures — tests you'd
write by hand anyway the moment a second implementation has to prove it
behaves like the first. What dbstore actually adds on top of that is the
10-line YAML and the two backend bodies; the 87 generated lines replace
work you would otherwise hand-write and keep in sync yourself every time
the interface changes.

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

## Single-Backend Quick Start

Use this direct form when repository portability is not needed. Here the
repository struct and methods are intentionally hand-written; `dbstore-gen` is
not involved.

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

// userRepo is the application-owned repository. source is a named field —
// never embedded, or Run would get promoted onto userRepo itself and leak
// straight past this file's own Create/FindByID methods.
type userRepo struct {
	source sqlxadapter.Source
}

func NewUserRepo(source sqlxadapter.Source) *userRepo {
	return &userRepo{source: source}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
	// a is a sqlxadapter.Handle, not a *sqlx.DB — it owns dialect rebinding
	// and sql.ErrNoRows translation so repository code never touches either.
	return r.source.Run(ctx, func(ctx context.Context, a sqlxadapter.Handle) error {
		return a.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name)
	})
}

func (r *userRepo) FindByID(ctx context.Context, id int) (string, error) {
	var name string
	err := r.source.Run(ctx, func(ctx context.Context, a sqlxadapter.Handle) error {
		return a.Get(ctx, &name, `SELECT name FROM users WHERE id = ?`, id)
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

	users := NewUserRepo(sql.Source("primary"))
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
    D["Executor[T]<br/>low-level, throttled access to the current named client"]
    E["adapter Source<br/>stable entry binding; named field on a repository, never embedded"]
    H["Handle<br/>backend-specific handle Source.Run hands to a callback — never the raw client"]
    F[Repository Implementation]
    G[Repository Interface]

    A --> B --> C --> D --> E --> H --> F --> G
```

Each layer depends only on the one below it — a repository implementation
touches its `Handle`, never `*sqlx.DB`/`*restadapter.Client` directly; a
`Backend` (see "Code Generator And Capabilities" below) touches its
`Handle`, never `Directory`/`Executor` internals. The application owns
repository interfaces, repository implementations, and backend-specific
operations. dbstore owns source registration, lifecycle, throttling, and
scoped client access, and stops there. Everything below — `Config` files,
transactions, REST/OpenSearch/Elasticsearch, custom drivers — builds on
this same shape.

## Guarantees

These are the concrete promises the chain above makes — verified by tests in
`internal/store` (including under `-race`), not just asserted here. Each one
is one line; expand for the precise semantics.

- **Visibility** — a source is visible only once `Open` succeeds.
- **Safe removal** — no new `Run` starts against a name after `Remove`
  returns; in-flight `Run`s finish first.
- **No double-open** — concurrent opens of the same name: exactly one wins.
- **Terminal close** — `Close` waits for in-flight Runs and Opens to finish
  cleanup; a source cannot publish after it returns, and later Opens fail.
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
- **Terminal close** — `Close` permanently closes the Adapter. It removes and
  drains registered sources, waits for any driver `Open` already in progress
  to finish and clean up its client, and rejects future `Open` calls. In
  particular, an `Open` racing with `Close` cannot publish a source after
  `Close` returns.
- **`Configure` publishes sequentially and rolls back what it opened, but
  isn't atomic** — sources are opened one at a time; if any fails, every
  source *this call* already opened is closed again before the error
  returns. Sources opened earlier in the same call are genuinely visible in
  the window before that rollback — a concurrent `Run` could reach them. A
  rollback `Remove`'s own error (e.g. `Close` failing) is discarded; only
  the triggering `Open` error is returned. Rollback is entry-identity based:
  if another goroutine removes one of these sources and registers a replacement
  under the same name, that replacement is left untouched. A name colliding
  with a source from an *earlier* `Configure` (or `Open`) call is also left
  untouched.
- **An Observer can't crash the operation it's observing** — a panicking
  `Observer` method is recovered and discarded (see "Metrics" below); it
  cannot fail a `Run` whose `fn` succeeded or make a successful `Register`
  look like it failed.
- **An Observer must not call back into the same `Directory`** — calling
  `Register`/`Remove`/`RemoveAll` on the source's own `Directory` from inside
  an Observer method is forbidden synchronous reentrancy and can self-deadlock
  the lifecycle callback order. Schedule that work after the callback returns.

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
github.com/loykin/dbstore/cmd/dbstore-gen        domain interface -> Backend/wrapper generator
github.com/loykin/dbstore/mcpserver              embeddable MCP server for SQL sources
github.com/loykin/dbstore/cmd/dbstore-mcp        ready-to-run MCP STDIO server
```

The root package has no SQL or REST dependency. Backend-specific helpers live
under `adapters/`.

## MCP Server

`mcpserver` exposes an existing `sqlxadapter.Adapter` through the official
Model Context Protocol Go SDK. The caller retains ownership of the Adapter:
embedding the server in an application shares that application's real
connection pools, throttles, and in-flight lifecycle guarantees.

```go
server, err := mcpserver.New(mcpserver.Options{
	Store: sqlAdapter,
})
if err != nil {
	log.Fatal(err)
}
if err := server.ServeStdio(ctx); err != nil {
	log.Fatal(err)
}
```

The default policy is inspection-only. It provides:

- `db_list_sources` — redacted source metadata, never DSNs
- `db_ping` — connectivity and `database/sql` pool statistics
- `db_list_tables` and `db_describe_table` — SQLite/PostgreSQL/MySQL schema inspection
- `db_query` — one row- and byte-bounded `SELECT`, executed through
  `Executor.Run`; denied until the Policy explicitly enables it
- `db_remove_source` — added only with `EnableManagement`, then
  still denied by the default policy

Source registration is added only with `EnableManagement` and a
`SourceConfigResolver`. The Store itself must implement `SourceManager`,
ensuring lifecycle operations target the same registry used for queries. The
MCP caller sends an opaque `configRef`, not a DSN or password. A custom
`Policy` must authorize management operations.

```go
server, err := mcpserver.New(mcpserver.Options{
	Store:            sqlAdapter,
	EnableManagement: true,
	Policy:           appPolicy,
	SourceResolver:   mcpserver.SourceConfigResolverFunc(
		func(ctx context.Context, ref string) (dbstore.SourceConfig, error) {
			return secretManager.ResolveDatabaseConfig(ctx, ref)
		},
	),
})
```

The package's SELECT validation, timeouts, row limits, and encoded-result byte
limits are defense in depth, not a SQL security boundary. `SELECT` can invoke
database functions with side effects, so query access is opt-in and its MCP
annotation is deliberately not marked read-only. Deploy MCP-accessible sources
with narrowly granted database credentials and use `Policy` for caller/source
authorization.

### Ready-to-run binary

```bash
go install github.com/loykin/dbstore/cmd/dbstore-mcp@latest

DBSTORE_MCP_DSN='file:dbstore.db' \
  dbstore-mcp -driver sqlite -source primary -allow-query
```

The binary uses STDIO and does not accept a DSN flag, avoiding command-line
credential exposure. With `-allow-manage`, a registration `configRef` such
as `ANALYTICS` resolves only from
`DBSTORE_MCP_SOURCE_ANALYTICS='{"driver":"postgres","dsn":"..."}'`.
Use the embeddable package with a real secret manager for production.

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

A source is the runtime handle kept by repository implementations, always as
a named field — **never embedded** (embedding promotes `Run` onto the
repository itself, leaking infra access past the repository's own
interface). The repository stays application-owned; dbstore only provides
scoped access to the registered backend client.

There are two levels of `Source`:

- **`dbstore.Source[T]`** (core) hands the callback the raw client `T`
  directly. This is the low-level primitive — reach for it only when
  writing a custom backend adapter, or as a deliberate escape hatch (see
  the FAQ below).
- **An adapter `Source`** (`sqlxadapter.Source`, `restadapter.Source`, ...)
  wraps `dbstore.Source[T]` and hands the callback a **`Handle`** instead
  — a backend-specific type that owns dialect/protocol details (SQL
  rebinding, not-found translation) so repository code never imports
  `database/sql` or a protocol package directly. This is what repository
  code should use.

```go
type userRepo struct {
	source sqlxadapter.Source
}

func NewUserRepo(source sqlxadapter.Source) *userRepo {
	return &userRepo{source: source}
}

func (r *userRepo) FindName(ctx context.Context, id int) (string, error) {
	var name string
	err := r.source.Run(ctx, func(ctx context.Context, a sqlxadapter.Handle) error {
		return a.Get(ctx, &name, `SELECT name FROM users WHERE id = $1`, id)
	})
	return name, err
}
```

`Executor.Run` is the lowest-level primitive of all — it's what both kinds
of `Source` are built on. Repository code should normally use an adapter
`Source`, not `Executor.Run` or `dbstore.Source[T]` directly.

## SQL Adapter

Use `adapters/sqlx` when the backend client is `*sqlx.DB`.

```go
import sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
```

```go
sql := sqlxadapter.New()
sql.RegisterDefaultDrivers()
```

Register drivers once, before the first valid source-registration attempt.
That first attempt freezes the registry before opening the client; later
registration panics as setup misuse instead of changing live runtime behavior.
The registry remains frozen even when that first client Open fails; retry the
source with corrected connection configuration, not a newly injected driver.
Registering the same driver name twice is also rejected rather than silently
replacing the first definition.

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

Transactional behavior belongs in the SQL `RepoBackend`, expressed through
`Handle.WithTx`. The domain repository interface remains transaction-free.

```go
func (SqliteAccountBackend) Transfer(ctx context.Context, h sqlxadapter.Handle, from, to int, amount int64) error {
	return h.WithTx(ctx, func(tx sqlxadapter.TxHandle) error {
		if err := tx.Exec(ctx, `UPDATE accounts SET balance = balance - ? WHERE id = ?`, amount, from); err != nil {
			return err
		}
		return tx.Exec(ctx, `UPDATE accounts SET balance = balance + ? WHERE id = ?`, amount, to)
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
	index  string
}

func NewDocumentRepo(source restadapter.Source, index string) *documentRepo {
	return &documentRepo{
		source: source,
		index:  index,
	}
}

func (r *documentRepo) Create(ctx context.Context, id, name string) error {
	// a is a restadapter.Handle — Post/Put/Get/Delete already translate a
	// 404 into dbstore.ErrNotFound, so repository code doesn't inspect
	// *restadapter.StatusError itself.
	return r.source.Run(ctx, func(ctx context.Context, a restadapter.Handle) error {
		return a.Put(ctx, "/"+r.index+"/_create/"+id, Document{Name: name})
	})
}
```

## Repository Contracts

dbstore does not define repository contracts. Applications do.

For the complete recommended workflow—from the domain interface through
generated backend implementations and the shared compliance suite—see
[`docs/repository-pattern.md`](docs/repository-pattern.md).

```go
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
}
```

Each backend implementation keeps the source that matches its client type
(see "Why" above for what running one compliance suite against all of them
buys you). `github.com/loykin/dbstore/dbstoretest` provides
`RunComplianceSuite` and `Fixture[R, C]` for the "run this suite once per named
fixture" loop — it doesn't know your contract or write assertions for you,
only the loop. `examples/repo_compliance` is a full, runnable version of
this: one `UserRepository`, a SQLite-backed and a REST-backed
implementation, and one test suite run against both via
`dbstoretest.RunComplianceSuite`.

## Code Generator And Capabilities

For a `UserRepository`-shaped contract that gets implemented against
several backends, hand-writing the delegation between the domain interface
and each `Handle` is a repetitive, easy-to-typo translation — exactly the
kind of thing worth generating instead of copying by hand. `cmd/dbstore-gen`
mirrors a hand-written domain interface into that glue:

Register the versioned generator once in the application module, then
declare what to generate in a YAML config — interface, source file, and
backend -> adapter mapping are always explicit data, never guessed from
file content or scattered CLI flags:

```sh
go get -tool github.com/loykin/dbstore/cmd/dbstore-gen@<version>
```

```yaml
# user_repo.gen.yaml
interface: UserRepository
source: user_repo.go
test: true
backends:
  - name: sqlite
    adapter: github.com/loykin/dbstore/adapters/sqlx
  - name: rest
    adapter: github.com/loykin/dbstore/adapters/rest
```

```go
//go:generate go tool dbstore-gen -config user_repo.gen.yaml
```

`adapter` may be a full import path or one of dbstore-gen's built-in short
names (`sqlite`, `mysql`, `postgres`, `rest`, `opensearch`,
`elasticsearch`). `-interface`/`-source`/`-backend` flags still exist for
one-off use outside a checked-in config, but `-interface` is always
required — dbstore-gen never infers which interface to target from file
content. An earlier version of this tool guessed it from a `*Repository`
name-suffix heuristic; that guess was quietly wrong exactly when it
mattered (the file's only interface being the wrong one), so it was removed
in favor of always reading it from a config or flag a human wrote down.

This generates, once per run, `user_repo_gen.go` — a `UserRepoBackend[A]`
interface plus the generic `userRepo[A]` wrapper that delegates every
`UserRepository` method through `dbstore.Call`/`dbstore.Exec` — and, the
first time only, one Backend stub per backend with method signatures already
filled in and `panic("TODO: implement")` bodies for you to replace.

`Call` returns `(zero value, dbstore.ErrNotFound)` for a miss. This is the one
generated repository rule for every value-returning method: method names and
per-method behavior are not duplicated in YAML. Applications that want to
hide the sentinel translate it at their service/API boundary.

With `test: true`, it also creates an application-owned compliance-suite
skeleton and one application-owned fixture stub per backend, while regenerating
`user_repo_compliance_gen_test.go` on every run. That generated registry is the
only fixture list: adding a backend in YAML therefore cannot leave it out of
the suite accidentally. The generator never overwrites the suite, fixture, or
Backend implementation files.

The generated files are always safe to regenerate. If the domain interface
later gains a method, generation stops before writing and prints copy-ready
method stubs for every incomplete existing Backend. Implement those methods,
then rerun generation to update the glue and scaffold newly configured
backends. Embedded interfaces, named results, and a variadic final parameter
are supported; methods must still take `context.Context` first and return
either `error` or `(value, error)`. See `AGENTS.md`'s "Adding a domain
repository" section for the full workflow. `examples/repository` and
`examples/repo_compliance` are complete, runnable versions with committed
configs.

The tool dependency keeps generation reproducible without requiring a global
binary installation.

`dbstoretest.Fixture[R, C]` carries an application-owned capability value
alongside its `New` constructor, so one compliance suite can assert a
guarantee — like "`CreateBatch` rolls back completely on failure"
— only against the fixtures whose backend can actually honor it, instead of
either failing the fixtures that can't or silently never checking the ones
that can. The application defines `C` because optional guarantees belong to
its repository contract; dbstoretest only passes the value through:

```go
var sqliteUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "SQLite", New: sqliteFixture,
	Caps: userRepoCapabilities{AtomicBatch: true},
}

var restUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "REST", New: restFixture, // Caps zero-value: no transaction concept
}
```

`dbstore-gen` places both variables in the generated fixture registry; the
application only fills their constructors, capabilities, and shared assertions.

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

Repositories use `opensearchadapter.Handle`/`elasticsearchadapter.Handle`,
not the SDK client directly — neither has a `WithTx`, since neither backend
has a transaction concept, so that capability simply isn't in the type's
method set:

```go
type documentRepo struct {
	source opensearchadapter.Source
	index  string
}

func (r *documentRepo) FindByID(ctx context.Context, id string) (*Document, error) {
	var doc Document
	err := r.source.Run(ctx, func(ctx context.Context, a opensearchadapter.Handle) error {
		return a.Get(ctx, r.index, id, &doc) // 404/missing -> dbstore.ErrNotFound
	})
	return &doc, err
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
	ObserveSourceRegistered(source string)
	ObserveSourceRemoved(source string)
	ObserveAcquire(source string, waited time.Duration, err error)
	ObserveComplete(source string, duration time.Duration, err error)
}
```

`ObserveAcquire`/`ObserveComplete` bracket `fn`'s execution (acquire
succeeds → run → complete), which is what lets an Observer track in-flight
operations, not just their duration afterward. All four methods are called
synchronously — the same constraint `net/http/httptrace.ClientTrace`'s hooks
document — so an implementation must not block or do I/O.

The Observer is fixed at construction, before any source can open. There is no
runtime replacement or state-resynchronization API, so every successful
registration has exactly one matching lifecycle event.

Core has no metrics dependency — `Observer` is vendor-neutral for the same
reason `PoolConfigApplier`/`Closer` are. `adapters/prometheus` is a
ready-made, comprehensive implementation; wiring it in is one call, and
every source registration and `Run` after that is automatically recorded:

```go
import prometheusadapter "github.com/loykin/dbstore/adapters/prometheus"

sql := sqlxadapter.New(sqlxadapter.WithObserver(
	prometheusadapter.New("myapp_sql", nil), // nil -> prometheus.DefaultRegisterer
))
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
plain logging implement `Observer` themselves instead. Combine more than one
(e.g. Prometheus metrics and a custom audit log) at construction with
`sqlxadapter.WithObserver(dbstore.MultiObserver{a, b})`. Omitting the option
costs nothing — no metrics dependency is loaded unless `adapters/prometheus`
is imported. See `examples/prometheus` for a full run scraping `/metrics`.

Observer callbacks are synchronous but not globally serialized: different
goroutines may call the same Observer concurrently, and Run callbacks may
overlap lifecycle callbacks. Implementations must be concurrency-safe.

## Dynamic Sources

Sources can be added and removed at runtime — e.g. opening one per tenant on
first use and tearing it down when the tenant disconnects.

```go
sql.Open("tenant-"+id, cfg)
repo := NewUserRepo(sql.Source("tenant-" + id))

// ...later, when the tenant is done:
err := sql.Remove("tenant-" + id)
```

`Remove` waits for that source's in-flight `Run` calls to finish and closes
its client, without touching any other source. A new `Run` against a removed
name fails immediately; the same name can be `Open`ed again afterward.

A `Source` binds to the exact registered entry that exists when the Source is
constructed. Removing that entry invalidates retained repositories; reopening
the same string name does not silently retarget them. Construct a fresh Source
and repository after a successful reopen:

```go
sql.Open("tenant-"+id, replacementCfg)
repo = NewUserRepo(sql.Source("tenant-" + id))
```

Construct Sources only after `Open` succeeds. A Source constructed before its
name is registered stays invalid rather than binding later. `Executor.Run`
remains the explicit low-level live-name operation for infrastructure setup;
repository code should use a Source. `Close`, unlike `Remove`, is terminal and
does not allow reopening.

## Examples

```text
examples/basic             SQLite driver registration with sqlxadapter
examples/sql_drivers       SQLite and PostgreSQL driver registration
examples/custom_sql_driver custom SQL driver registration with sqlxadapter
examples/rest              custom REST driver registration with restadapter
examples/custom_adapter    custom backend client registration with dbstore.NewAdapter[T]
examples/opensearch        OpenSearch SDK client registration
examples/elasticsearch     Elasticsearch SDK client registration
examples/repository        dbstore-gen generated UserRepository over sqlxadapter.Handle
examples/multi_db          multiple named SQL sources
examples/sqlite_concurrent SQLite concurrency throttling
examples/config            Config-driven setup spanning SQL and REST sources
examples/repo_compliance   dbstore-gen generated UserRepository, SQLite + REST, one capability-gated suite
examples/prometheus        construction-time Observer wired to Prometheus metrics
```

## Layout

```text
dbstore.go             public core API
internal/store         core implementation (Runner[T], Exec/Call, ErrNotFound live here)
adapters/sqlx          SQL/sqlx adapter, Source, Handle/TxHandle, pool config
adapters/rest          REST adapter, Source, Handle, client helpers
adapters/opensearch    OpenSearch adapter, driver, Source, Handle
adapters/elasticsearch Elasticsearch adapter, driver, Source, Handle
adapters/prometheus    Observer implementation backed by Prometheus metrics
dbstoretest            RunComplianceSuite/Fixture[R,C] test helper
cmd/dbstore-gen        domain interface -> RepoBackend[A]/generic wrapper generator
examples               runnable examples
```

## FAQ

**How do I run operations across two repositories in one transaction?**

dbstore doesn't provide this through `Handle` — each repository's `source`
field is private, so a use case coordinating two repositories has no
`Handle`/`TxHandle` value to hand to both of them. That's intentional:
dbstore stops at lifecycle and scoped access, and cross-repository
transaction coordination is operation semantics, which it deliberately
leaves to the application (see "Why").

The fix is to add a second, explicitly-lower-level method that takes a raw
`*sqlx.Tx` instead of going through `Handle` — a deliberate, visible
bypass, not a default path. `sqlxadapter.RunTx` (a free function, unrelated
to `Handle.WithTx`) is kept exactly for this:

```go
// Normal path: Source.Run hands it a sqlxadapter.Handle.
func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.source.Run(ctx, func(ctx context.Context, a sqlxadapter.Handle) error {
		return a.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name)
	})
}

// Escape-hatch path: takes a raw *sqlx.Tx directly, bypassing Handle —
// only a use case coordinating multiple repositories should call this.
func (r *userRepo) createInTx(ctx context.Context, tx *sqlx.Tx, name string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
	return err
}

// A use case gets a *sqlx.Tx from sqlxadapter.RunTx and passes it directly
// into both repositories' "InTx" methods, so both writes share one
// transaction.
func (uc *signupUseCase) RegisterUser(ctx context.Context, name string) error {
	return sqlxadapter.RunTx(uc.exec, ctx, "primary", func(ctx context.Context, tx *sqlx.Tx) error {
		if err := uc.users.createInTx(ctx, tx, name); err != nil {
			return err
		}
		return uc.accounts.grantInTx(ctx, tx, name, 100)
	})
}
```

This only works within one named SQL source — a `*sqlx.Tx` belongs to one
`*sqlx.DB`, so it can't span two different named sources (e.g. `"primary"`
and `"replica"`), let alone SQL and REST. That's a limit of SQL transactions
themselves, not something dbstore could paper over.
