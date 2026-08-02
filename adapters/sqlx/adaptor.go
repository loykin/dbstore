package sqlxadapter

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// Adaptor is the only handle a UserRepoTemplate-style backend
// implementation ever sees for a SQL source — it owns dialect rebinding
// (sqlx.Rebind) and sql.ErrNoRows translation so Template code never needs
// to import "database/sql" or know which dialect it is talking to.
type Adaptor struct{ db *sqlx.DB }

// Get runs a single-row query and scans it into dest. A sql.ErrNoRows is
// translated into dbstore.ErrNotFound, which dbstore.Call turns into a
// (zero, nil) result for the caller.
func (a Adaptor) Get(ctx context.Context, dest any, query string, args ...any) error {
	err := a.db.GetContext(ctx, dest, a.db.Rebind(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return dbstore.ErrNotFound
	}
	return err
}

// Select runs a multi-row query and scans it into dest (a pointer to a slice).
func (a Adaptor) Select(ctx context.Context, dest any, query string, args ...any) error {
	return a.db.SelectContext(ctx, dest, a.db.Rebind(query), args...)
}

// Exec runs a statement that returns no rows.
func (a Adaptor) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.db.ExecContext(ctx, a.db.Rebind(query), args...)
	return err
}

// WithTx runs fn inside a transaction, committing on a nil return and
// rolling back otherwise. fn receives a TxAdaptor with the same Get/Select/
// Exec surface as Adaptor, so Template code doesn't branch on whether it's
// inside a transaction.
func (a Adaptor) WithTx(ctx context.Context, fn func(TxAdaptor) error) error {
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(TxAdaptor{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// TxAdaptor is the transactional counterpart of Adaptor, handed to the
// callback passed to Adaptor.WithTx.
type TxAdaptor struct{ tx *sqlx.Tx }

func (a TxAdaptor) Get(ctx context.Context, dest any, query string, args ...any) error {
	err := a.tx.GetContext(ctx, dest, a.tx.Rebind(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return dbstore.ErrNotFound
	}
	return err
}

func (a TxAdaptor) Select(ctx context.Context, dest any, query string, args ...any) error {
	return a.tx.SelectContext(ctx, dest, a.tx.Rebind(query), args...)
}

func (a TxAdaptor) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.tx.ExecContext(ctx, a.tx.Rebind(query), args...)
	return err
}
