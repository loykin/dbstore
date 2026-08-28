// Generated skeleton by dbstore-gen for backend "sqlite" —
// created once, never overwritten. Fill in the TODO bodies; the signatures
// already match UserRepoBackend[sqlxadapter.Handle], so a
// future UserRepository method addition shows up here as a compile
// error (see the "var _ ..." assertion in the _gen.go file), not silently.

package main

import (
	"context"

	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
)

type SqliteUserBackend struct{}

// NewSqliteUserRepository is the application-facing constructor. Repository
// wiring stays generated; this file only owns SQLite behavior and dependencies.
func NewSqliteUserRepository(source sqlxadapter.Source) UserRepository {
	return NewUserRepo[sqlxadapter.Handle](SqliteUserBackend{}, source)
}

func (SqliteUserBackend) Create(ctx context.Context, h sqlxadapter.Handle, name string) error {
	return h.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name)
}

func (SqliteUserBackend) CreateBatch(ctx context.Context, h sqlxadapter.Handle, names []string) error {
	return h.WithTx(ctx, func(tx sqlxadapter.TxHandle) error {
		for _, name := range names {
			if err := tx.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

func (SqliteUserBackend) FindAll(ctx context.Context, h sqlxadapter.Handle) ([]User, error) {
	var users []User
	err := h.Select(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	return users, err
}

func (SqliteUserBackend) FindByID(ctx context.Context, h sqlxadapter.Handle, id int) (*User, error) {
	var user User
	err := h.Get(ctx, &user, `SELECT id, name FROM users WHERE id = ?`, id)
	return &user, err
}
