# Generated repository example

This example shows the complete `dbstore-gen` workflow for one SQLite-backed
`UserRepository`.

Run it with:

```sh
go generate ./...
go test ./...
go run .
```

`go generate ./...` is safe to run repeatedly. File ownership is explicit:

| File | Owner | Regeneration behavior |
|---|---|---|
| `user_repo_gen.go` | dbstore-gen | Replaced on every run; do not edit |
| `user_repo_compliance_gen_test.go` | dbstore-gen | Replaced on every run; do not edit |
| `user_repo_sqlite.go` | Application | Created once, then never overwritten |
| `user_repo_compliance_test.go` | Application | Created once, then never overwritten |
| `user_repo_sqlite_test.go` | Application | Created once, then never overwritten |

The application defines the domain interface in `user_repo.go`, implements
SQLite behavior in `user_repo_sqlite.go`, and writes behavioral assertions in
`user_repo_compliance_test.go`. The generated test registry ensures every
backend listed in `user_repo.gen.yaml` participates in that suite.

When `UserRepository` gains a method, generation stops without overwriting the
application-owned files and prints the missing backend method stub. Add that
method to `SqliteUserBackend`, then run generation again.
