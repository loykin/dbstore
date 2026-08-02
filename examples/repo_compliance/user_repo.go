package main

import "context"

//go:generate dbstore-gen -interface UserRepository -source user_repo.go -backend sqlite:github.com/loykin/dbstore/adapters/sqlx -backend rest:github.com/loykin/dbstore/adapters/rest

// User is the domain model both backends below implement.
type User struct {
	ID   int    `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

// UserRepository is the one contract both backends implement. The
// compliance suite in main_test.go only ever calls these four methods, so
// it runs unchanged against either implementation — the concrete proof for
// the root README's "Why" claim.
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
	CreateBatch(ctx context.Context, names []string) error
}
