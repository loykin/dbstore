package sqlxadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"
)

func newTestAdaptor(t *testing.T) Adaptor {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	return Adaptor{db: db}
}

func TestAdaptor_Get_NotFoundTranslatesToErrNotFound(t *testing.T) {
	a := newTestAdaptor(t)
	var name string
	err := a.Get(context.Background(), &name, `SELECT name FROM users WHERE id = ?`, 999)
	if !errors.Is(err, dbstore.ErrNotFound) {
		t.Fatalf("want dbstore.ErrNotFound, got %v", err)
	}
}

func TestAdaptor_Get_Found(t *testing.T) {
	a := newTestAdaptor(t)
	if err := a.Exec(context.Background(), `INSERT INTO users (name) VALUES (?)`, "Alice"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := a.Get(context.Background(), &name, `SELECT name FROM users WHERE id = ?`, 1); err != nil {
		t.Fatal(err)
	}
	if name != "Alice" {
		t.Fatalf("name = %q, want Alice", name)
	}
}

func TestAdaptor_WithTx_CommitsOnSuccess(t *testing.T) {
	a := newTestAdaptor(t)
	err := a.WithTx(context.Background(), func(tx TxAdaptor) error {
		return tx.Exec(context.Background(), `INSERT INTO users (name) VALUES (?)`, "Bob")
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.Get(context.Background(), &count, `SELECT COUNT(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestAdaptor_WithTx_RollsBackOnError(t *testing.T) {
	a := newTestAdaptor(t)
	sentinel := errors.New("intentional")
	err := a.WithTx(context.Background(), func(tx TxAdaptor) error {
		if execErr := tx.Exec(context.Background(), `INSERT INTO users (name) VALUES (?)`, "ShouldRollback"); execErr != nil {
			return execErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	var count int
	if err := a.Get(context.Background(), &count, `SELECT COUNT(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 after rollback", count)
	}
}
