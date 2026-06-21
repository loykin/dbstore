package store

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// BaseRepo provides query execution against a named datasource.
// Embed it in a concrete repository struct.
type BaseRepo struct {
	name string
	exec *Executor
}

func NewBaseRepo(name string, exec *Executor) BaseRepo {
	return BaseRepo{name: name, exec: exec}
}

func (r *BaseRepo) Run(ctx context.Context, fn func(context.Context, *sqlx.DB) error) error {
	return r.exec.Run(ctx, r.name, fn)
}

func (r *BaseRepo) RunTx(ctx context.Context, fn func(context.Context, *sqlx.Tx) error) error {
	return r.exec.RunTx(ctx, r.name, fn)
}
