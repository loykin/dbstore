package sqlxadapter

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// Source is a stable entry binding that hands repository backend code a Handle
// instead of the raw *sqlx.DB. Value receiver (not pointer) so a Source value
// satisfies dbstore.Runner[Handle].
type Source struct {
	source dbstore.Source[*sqlx.DB]
}

func NewSource(name string, exec *dbstore.Executor[*sqlx.DB]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Handle] = Source{}

func (s Source) Run(ctx context.Context, fn func(context.Context, Handle) error) error {
	return s.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return fn(ctx, Handle{db: db})
	})
}

// RunTx is the low-level escape hatch for direct transaction access outside
// the Handle/Backend pattern — it hands back the raw *sqlx.Tx, unlike
// Handle.WithTx which stays inside the Handle/TxHandle vocabulary.
func (s Source) RunTx(ctx context.Context, fn func(context.Context, *sqlx.Tx) error) error {
	return s.source.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return runTx(ctx, db, fn)
	})
}

// RunTx executes fn within a transaction against the *sqlx.DB registered under
// name. It uses dbstore.Executor.Run so source throttling and lifecycle rules
// remain owned by the core runtime.
func RunTx(exec *dbstore.Executor[*sqlx.DB], ctx context.Context, name string, fn func(context.Context, *sqlx.Tx) error) error {
	return exec.Run(ctx, name, func(ctx context.Context, db *sqlx.DB) error {
		return runTx(ctx, db, fn)
	})
}

func runTx(ctx context.Context, db *sqlx.DB, fn func(context.Context, *sqlx.Tx) error) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
