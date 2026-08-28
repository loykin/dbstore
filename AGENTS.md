# AGENTS.md

Guidance for coding agents (Claude Code, Codex, etc.) working in this
repository. `CLAUDE.md` imports this file directly.

## Commands

- Build: `go build ./...`
- Vet: `go vet ./...`
- Format: `gofmt -l .` (check) / `gofmt -w .` (apply)
- Unit tests, matches CI: `go test -race -count=1 -timeout=120s ./...`
- Single test: `go test ./internal/store/ -run TestName -v`
- Integration tests (Postgres/MySQL/OpenSearch/Elasticsearch via
  testcontainers-go, needs Docker):
  `go test -tags integration -timeout=600s ./internal/store/ ./adapters/opensearch/... ./adapters/elasticsearch/...`
- Chaos tests (skipped unless env-gated):
  `DBSTORE_CHAOS_TEST=1 go test -run TestDirectory_Chaos_GoroutineLeaks -timeout=600s ./internal/store/`
  (also `TestDirectory_Chaos_MemoryStability`)
- Fuzz targets: `go test -fuzz=FuzzDirectory_Register -fuzztime=30s ./internal/store/`
  (also `FuzzDirectory_AcquireRelease`, `FuzzExecutor_Run`, `FuzzThrottle_Concurrency`)
- Lint: `golangci-lint run --timeout=5m` (CI uses golangci-lint-action@v6
  with the default ruleset — no local `.golangci.yml`)
- Examples are independent modules, not covered by the root `./...` — see
  "Examples are independent Go modules" below. From inside `examples/<name>/`:
  `go test ./... && go run .`

## Architecture

### Facade over internal/store

The root `dbstore` package (`dbstore.go`) is a thin type-alias facade:
`dbstore.Directory[T]`, `dbstore.Executor[T]`, etc. are Go type aliases
(`=`) for `internal/store` types, not wrappers — they're identical types.
All real logic lives in `internal/store`; read there first when tracing
behavior. The root package exists only to give a shorter public import path
plus the `NewAdapter`/`NewSource` constructors.

### The registration → access chain

Runtime setup flows from `DriverBuilder[T]` through `Adapter[T]`,
`Directory[T]`, and `Executor[T]`. A repository call flows in the other
direction: `application repository -> generated wrapper -> Source -> Handle
-> RepoBackend`. Each boundary depends only on the layer immediately below it:

- `DriverBuilder[T]` opens one concrete `T` from a `SourceConfig`.
- `Directory[T]` (`internal/store/directory.go`) owns the name -> client
  map, lifecycle (`Register`/`Remove`/`RemoveAll`), and a per-source
  concurrency throttle.
- `Executor[T]` (`executor.go`) is the scoped, throttled entry point
  repository code calls through `Run`.
- `Adapter[T]` (`adapter.go`) is the public-facing wrapper combining a
  `DriverRegistry` + `Directory` + `Executor`. `AdapterContract[T]` is a
  compile-time-only interface (`var _ AdapterContract[T] = (*Adapter)(nil)`)
  that keeps all four concrete adapters (sqlx/rest/opensearch/elasticsearch)
  implementing the same method set in sync — add a method there when adding
  one to `Adapter[T]`, and update all four adapter packages together.
  `Source(name)` is deliberately *not* on this interface — see
  `adapters/sqlx/adapter.go`'s doc comment for why.
- `Source[T]` is the low-level `Runner[T]` core repositories can use
  directly; `sqlxadapter.Source`/`restadapter.Source` wrap it to hand out an
  `Handle` instead of the raw client (see "Adding a domain repository"
  below). **Never embed a `Source` or `Handle` in a repository struct** —
  always a named field. Embedding promotes `Run` onto the repository type
  itself, leaking infra access past the domain interface; `examples/`
  previously had exactly this bug.

### Adding a domain repository (multi-backend)

The workflow is:

1. Register the generator with `go get -tool
   github.com/loykin/dbstore/cmd/dbstore-gen@<version>`, define the domain
   interface once (e.g. `UserRepository`), then write a YAML config
   declaring what to generate — interface, source file, and backend ->
   adapter mapping are always explicit data, never inferred from file
   content or CLI flags. An earlier version guessed the interface from a
   `*Repository` name-suffix heuristic; that guess was quietly wrong
   exactly when it mattered (the file's only interface being the wrong
   one), so it was removed in favor of always reading it from a config or
   flag a human wrote down:

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

   `adapter` may be a full import path or one of `dbstore-gen`'s built-in
   short names (`sqlite`, `mysql`, `postgres`, `rest`, `opensearch`,
   `elasticsearch`). This generates `x_gen.go` (`XRepoBackend[A]` + the generic
   wrapper — DO NOT EDIT, regenerated every run) and, the first time only, a
   `XxxBackend` stub per backend with an application-facing constructor,
   `panic("TODO: implement")` bodies, and correct signatures. Embedded
   interfaces, named results, and a variadic final parameter are supported;
   methods must take `context.Context` first and return `error` or
   `(value, error)`.
   (`-interface`/`-source`/`-backend` flags still work
   for quick one-off use outside a checked-in config, but `-interface` is
   always required then — dbstore-gen never guesses it.)
2. Fill in each backend's `XxxBackend` method bodies using that backend's
   `Handle` type (`sqlxadapter.Handle`, `restadapter.Handle`, ...) —
   never the raw client. `Handle.Get` already translates a driver
   "not found" (`sql.ErrNoRows`, HTTP 404, ...) into `dbstore.ErrNotFound`;
   `dbstore.Call` translates that into `(nil, nil)` for the caller, so
   repository backend code should not do its own not-found translation beyond
   returning what `Handle.Get` gives it.
3. Document a multi-op method's atomicity by which `Handle` method it uses
   — `WithTx` (SQL, atomic) vs. a plain loop (no transaction concept,
   best-effort sequential). Reflect this in the corresponding
   application-owned capability type and set it on the corresponding
   `dbstoretest.Fixture`; this lets the compliance suite assert rollback
   only against fixtures that can actually guarantee it.
4. With `test: true`, generation creates an application-owned compliance-suite
   skeleton and one application-owned `dbstoretest.Fixture` stub per backend.
   Fill those files using only the domain interface's methods and set the
   application-owned capabilities. Do not hand-edit
   `x_compliance_gen_test.go`: it is regenerated from the configured backend
   list so a newly configured backend cannot be omitted from the suite. See
   `examples/repo_compliance/user_repo_compliance_test.go` and the adjacent
   `user_repo_{sqlite,rest}_test.go` fixture files.
5. `go generate ./... && go build ./... && go test ./...` before finishing.
   When the domain interface gains a method, generation stops before writing
   anything and prints copy-ready stubs for the incomplete Backends. Add the
   missing method to each existing implementation, then regenerate; do not
   delete the files.
   The generated `var _ XRepoBackend[...] = ...Backend{}` assertions remain a
   second compile-time guard for signature mismatches.

### Directory's two-lock design

Read this before touching lifecycle or Observer code — it has been the
source of several subtle concurrency bugs, each now covered by a dedicated
regression test in `observer_test.go`. `Directory[T]` uses two locks, not
one: `mu` guards the entries map, and `observerMu` (an `observerLock`, not
a plain `sync.Mutex` — see `observer_lock.go`) orders Observer callback
delivery to match mutation order. `beginObserverHandoff()` is the single
choke point all four mutating methods (`Register`/`Remove`/`RemoveAll`/
`SetObserver`) go through to hand off from `mu` to `observerMu` without
leaking either lock if the handoff panics. `observerCallbackGuard` rejects
same-goroutine reentrancy before lifecycle state changes — including from
`ObserveAcquire`/`ObserveComplete`, which do not hold `observerMu` — while
`observerLock` retains a defensive lock-level check. See the doc comments on
the `observerMu` field, `beginObserverHandoff`, and `observer_lock.go` for
the reasoning before changing any of this.

### Adapters vs. Observer

`adapters/{sqlx,rest,opensearch,elasticsearch}` are backend adapters — each
wraps `Adapter[T]` for one concrete client type and provides
`RegisterDefaultDrivers`. `adapters/prometheus` is a different kind of
thing: an `Observer` implementation, not a backend adapter. It plugs into
any `Directory`/`Executor` via `SetObserver` and has no adapter-contract
obligations.

### dbstoretest is intentionally a separate package

`dbstoretest` imports `"testing"`; the root `dbstore` package does not.
Keeping it separate keeps `testing` out of the import graph of every
production binary depending on `dbstore` — same reasoning as
`net/http/httptest`.

### MCP is an embeddable package plus a thin binary

`mcpserver` is a public library that exposes a caller-owned
`sqlxadapter.Adapter` over MCP; it must not close that Adapter. All SQL work
goes through `Executor.Run` so the core throttle and removal guarantees still
apply. `cmd/dbstore-mcp` only owns process configuration, default SQL driver
imports, and STDIO startup. Keep reusable MCP tools, policy, credential
reference resolution interfaces, and tests in `mcpserver`, not under `cmd`.
MCP responses must never include a source DSN or credential.

### Examples are independent Go modules

Every directory under `examples/` has its own `go.mod` with
`replace github.com/loykin/dbstore => ../..`. `go build ./...` /
`go test ./...` at the repo root does not touch them, and they're not
picked up by `go vet ./...` either. Work inside one with
`cd examples/<name> && go test ./... && go run .`. CI runs each example
individually (see `.github/workflows/ci.yml`'s `examples` job) — add a new
step there when adding a new example.

## Design invariants to preserve

These are asserted by tests under `internal/store` (including `-race`), not
just documented — see the README's "Guarantees" section for the precise
semantics before changing `Directory`, `Adapter.Configure`, or `Observer`:

- A source is visible only once `Open`/`Register` actually succeeds.
- `Remove` waits for in-flight `Run` calls before closing the client; no
  new `Run` starts against a removed name.
- Concurrent opens of the same name: exactly one wins, the loser's client
  is closed rather than leaked.
- `Configure` is sequential-publish-with-rollback, not atomic.
- A panicking `Observer` method is recovered and must never fail the
  `Run`/`Register`/etc. call that triggered it.
