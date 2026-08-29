# Repository pattern

dbstore's main use case is one application-owned repository contract with
multiple backend implementations that are checked by the same behavioral test
suite.

The runtime does not try to make SQL, REST, OpenSearch, and Elasticsearch look
like one database. Instead, it standardizes source lifecycle and execution,
then keeps each backend's protocol logic explicit.

## Vocabulary

| Term | Responsibility |
|---|---|
| Repository interface | Application-owned domain contract, such as `UserRepository` |
| Adapter | Opens named sources and owns their lifecycle |
| Source | A `Runner` bound to the exact registered source entry present at construction |
| Handle | Backend-specific operations exposed to repository code; never the raw client |
| RepoBackend | One backend's implementation of the repository operations |
| Generated repository | Delegates the domain interface through a `Runner` to a `RepoBackend` |
| Compliance suite | Behavioral assertions run unchanged against every backend fixture |

`Adapter` and `Handle` are intentionally different. The adapter is the
application-level lifecycle entry point. A handle is the restricted value
passed to one repository operation.

## Data flow

```text
Repository method
  -> generated repository wrapper
  -> Source.Run
  -> backend Handle
  -> RepoBackend method
  -> raw client hidden inside the adapter package
```

The repository wrapper stores its `Source` as a named field. Do not embed a
`Source` or `Handle`: embedding promotes infrastructure methods onto the
repository type and weakens the domain boundary.

## 1. Define the domain contract

The application owns the interface and domain types.

```go
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
}
```

Keep the contract backend-neutral. Transaction objects, SQL strings, HTTP
status codes, and SDK request types do not belong in it.

## 2. Declare the backends

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

Run `go generate ./...`. The generator creates:

- `UserRepoBackend[H]`, the backend operation contract;
- one generic `UserRepository` implementation;
- a first-run backend stub such as `SqliteUserBackend` and its constructor;
- a first-run application-owned compliance-suite skeleton;
- one first-run application-owned fixture stub per backend;
- a regenerated test registry containing every configured backend fixture;
- compile-time assertions that every configured backend remains complete.

Generated glue and the test registry are overwritten on every run. Backend,
suite, and fixture implementation files are created once and never overwritten.
Adding a backend to the config creates its fixture stub and registers it in the
suite automatically.

The generator follows embedded interfaces and supports named results and a
variadic final parameter. Repository methods must take `context.Context` first
and return either `error` or `(value, error)`.

## 3. Implement each backend with its Handle

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

Repository backend code uses only the adapter's `Handle`. It does not receive
`*sqlx.DB`, `*http.Client`, or a search SDK client. Handles own protocol-level
normalization such as query rebinding and not-found translation.

`Handle.Get` returns `dbstore.ErrNotFound` for a missing record. Generated
repositories always return it with the result type's zero value through
`dbstore.Call`. Method
names and method-level policies are deliberately absent from the YAML, keeping
the Go interface as the single source of repository shape. Backend
implementations should return the Handle error rather than translating
not-found again.

## 4. Construct the repository

```go
sql := sqlxadapter.New()
sql.RegisterDefaultDrivers()

if err := sql.Open("primary", dbstore.SourceConfig{
	Driver: sqlxadapter.DriverSQLite,
	DSN:    ":memory:",
}); err != nil {
	return err
}

repo := NewSqliteUserRepository(sql.Source("primary"))
```

The backend-specific constructor and adapter's `Source(name)` method are the
preferred repository entry points. Direct `Executor.Run` access is appropriate
for infrastructure setup such as schema creation, but normal repository
operations should go through a Source and Handle.

When this repository is the only consumer of `"primary"`, the generated
`ConnectSqliteUserRepository` collapses the block above into one call:

```go
repo, closeFn, err := ConnectSqliteUserRepository(
	func(a *sqlxadapter.Adapter) { a.RegisterDefaultDrivers() },
	dbstore.SourceConfig{Driver: sqlxadapter.DriverSQLite, DSN: ":memory:"},
)
```

It opens its own adapter and connection pool, so it only makes sense while
nothing else shares that source name. As soon as a second repository needs
`"primary"` too, go back to the explicit `Open` + `Source` form so both
repositories share one pool and one throttle instead of each getting their
own.

## 5. Verify every backend with one suite

Write assertions only against the domain interface. Construct a fresh
repository for each nested test so state cannot leak between cases.

```go
type userRepoCapabilities struct {
	AtomicBatch bool
}

func runUserRepoComplianceSuite(
	t *testing.T,
	newRepo func(t *testing.T) UserRepository,
	caps userRepoCapabilities,
) {
	t.Run("FindByID_NotFound", func(t *testing.T) {
		repo := newRepo(t)
		user, err := repo.FindByID(context.Background(), 999)
		if !errors.Is(err, dbstore.ErrNotFound) || user != nil {
			t.Fatalf("got (%v, %v), want (nil, ErrNotFound)", user, err)
		}
	})
}

var sqliteUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "SQLite",
	New:  newSQLiteFixture,
	Caps: userRepoCapabilities{AtomicBatch: true},
}

var restUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "REST",
	New:  newRESTFixture,
}
```

The application defines `userRepoCapabilities`; dbstoretest only passes it
through generically. Capabilities describe real optional guarantees, not
backend identity. For example, test rollback only when `AtomicBatch` is true.
All ordinary behavior must still be asserted for every fixture. The generator
writes `TestUserRepoCompliance` and its fixture list into
`user_repo_compliance_gen_test.go`; do not edit that registry directly.

See `examples/repository` for one generated SQL implementation and
`examples/repo_compliance` for the same repository contract implemented over
both SQLite and REST.

## When to skip generation

For a small repository with one backend, a hand-written type containing an
adapter `Source` is sufficient. Use `dbstore-gen` when at least two backends
share a repository contract or when compile-time synchronization between the
domain interface and backend implementations is valuable.
