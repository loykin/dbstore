// Generated skeleton by dbstore-gen for backend "sqlite" —
// created once, never overwritten. Fill in the TODO bodies; the signatures
// already match UserRepoTemplate[sqlxadapter.Adaptor], so a
// future UserRepository method addition shows up here as a compile
// error (see the "var _ ..." assertion in the _gen.go file), not silently.

package main

import (
	"context"

	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
)

type SqliteUserTemplate struct{}

func (SqliteUserTemplate) Create(ctx context.Context, a sqlxadapter.Adaptor, name string) error {
	return a.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name)
}

func (SqliteUserTemplate) CreateBatch(ctx context.Context, a sqlxadapter.Adaptor, names []string) error {
	return a.WithTx(ctx, func(tx sqlxadapter.TxAdaptor) error {
		for _, name := range names {
			if err := tx.Exec(ctx, `INSERT INTO users (name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

func (SqliteUserTemplate) FindAll(ctx context.Context, a sqlxadapter.Adaptor) ([]User, error) {
	var users []User
	err := a.Select(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	return users, err
}

func (SqliteUserTemplate) FindByID(ctx context.Context, a sqlxadapter.Adaptor, id int) (*User, error) {
	var user User
	err := a.Get(ctx, &user, `SELECT id, name FROM users WHERE id = ?`, id)
	return &user, err
}
