package store

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// BaseRepo provides query execution against a named datasource for any
// client type T. Embed it in a concrete repository struct.
type BaseRepo[T any] struct {
	name string
	exec *Executor[T]
}

func NewBaseRepo[T any](name string, exec *Executor[T]) BaseRepo[T] {
	return BaseRepo[T]{name: name, exec: exec}
}

func (r *BaseRepo[T]) Run(ctx context.Context, fn func(context.Context, T) error) error {
	return r.exec.Run(ctx, r.name, fn)
}

// SQLRepo adds RunTx on top of BaseRepo[*sqlx.DB]. RunTx can't be a
// BaseRepo[T] method for arbitrary T (see RunTx in executor.go), so SQL
// repositories embed SQLRepo instead of BaseRepo[*sqlx.DB] directly when
// they need transactions.
type SQLRepo struct {
	BaseRepo[*sqlx.DB]
}

func NewSQLRepo(name string, exec *Executor[*sqlx.DB]) SQLRepo {
	return SQLRepo{NewBaseRepo(name, exec)}
}

func (r *SQLRepo) RunTx(ctx context.Context, fn func(context.Context, *sqlx.Tx) error) error {
	return RunTx(r.exec, ctx, r.name, fn)
}
