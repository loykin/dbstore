package sqlxadapter

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
)

// Handle is the only handle a UserRepoBackend-style backend
// implementation ever sees for a SQL source — it owns dialect rebinding
// (sqlx.Rebind) and sql.ErrNoRows translation so Backend code never needs
// to import "database/sql" or know which dialect it is talking to.
type Handle struct{ db *sqlx.DB }

// Get runs a single-row query and scans it into dest. A sql.ErrNoRows is
// translated into dbstore.ErrNotFound, which dbstore.Call turns into a
// (zero, nil) result for the caller.
func (a Handle) Get(ctx context.Context, dest any, query string, args ...any) error {
	err := a.db.GetContext(ctx, dest, a.db.Rebind(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return dbstore.ErrNotFound
	}
	return err
}

// Select runs a multi-row query and scans it into dest (a pointer to a slice).
func (a Handle) Select(ctx context.Context, dest any, query string, args ...any) error {
	return a.db.SelectContext(ctx, dest, a.db.Rebind(query), args...)
}

// Exec runs a statement that returns no rows.
func (a Handle) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.db.ExecContext(ctx, a.db.Rebind(query), args...)
	return err
}

// WithTx runs fn inside a transaction, committing on a nil return and
// rolling back otherwise. fn receives a TxHandle with the same Get/Select/
// Exec surface as Handle, so Backend code doesn't branch on whether it's
// inside a transaction.
func (a Handle) WithTx(ctx context.Context, fn func(TxHandle) error) error {
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(TxHandle{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// TxHandle is the transactional counterpart of Handle, handed to the
// callback passed to Handle.WithTx.
type TxHandle struct{ tx *sqlx.Tx }

func (a TxHandle) Get(ctx context.Context, dest any, query string, args ...any) error {
	err := a.tx.GetContext(ctx, dest, a.tx.Rebind(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return dbstore.ErrNotFound
	}
	return err
}

func (a TxHandle) Select(ctx context.Context, dest any, query string, args ...any) error {
	return a.tx.SelectContext(ctx, dest, a.tx.Rebind(query), args...)
}

func (a TxHandle) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.tx.ExecContext(ctx, a.tx.Rebind(query), args...)
	return err
}
