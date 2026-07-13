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

`DriverBuilder[T] -> Adapter[T] -> Directory[T] -> Executor[T] -> Source[T]
-> application repository`. Each layer depends only on the one below it:

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
- `Source[T]` is the handle a repository implementation embeds for a `Run`
  method.

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
