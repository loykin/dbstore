package sqlxadapter

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// Source hands Template code an Adaptor instead of the raw *sqlx.DB — see
// docs/design-codegen.md for why. Value receiver (not pointer) so a Source
// value satisfies dbstore.Runner[Adaptor].
type Source struct {
	source dbstore.Source[*sqlx.DB]
}

func NewSource(name string, exec *dbstore.Executor[*sqlx.DB]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Adaptor] = Source{}

func (s Source) Run(ctx context.Context, fn func(context.Context, Adaptor) error) error {
	return s.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return fn(ctx, Adaptor{db: db})
	})
}

// RunTx is the low-level escape hatch for direct transaction access outside
// the Adaptor/Template pattern — it hands back the raw *sqlx.Tx, unlike
// Adaptor.WithTx which stays inside the Adaptor/TxAdaptor vocabulary.
func (s Source) RunTx(ctx context.Context, fn func(context.Context, *sqlx.Tx) error) error {
	return RunTx(s.source.Executor(), ctx, s.source.Name(), fn)
}

// RunTx executes fn within a transaction against the *sqlx.DB registered under
// name. It uses dbstore.Executor.Run so source throttling and lifecycle rules
// remain owned by the core runtime.
func RunTx(exec *dbstore.Executor[*sqlx.DB], ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	return exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		if err := fn(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}
