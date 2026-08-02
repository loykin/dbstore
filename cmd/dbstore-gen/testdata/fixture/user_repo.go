// Package fixture is dbstore-gen's own test fixture, not an example for
// library consumers.
package fixture

import "context"

type User struct {
	ID   int
	Name string
}

type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	CreateBatch(ctx context.Context, names []string) error
}

type VariadicRepository interface {
	M(ctx context.Context, names ...string) error
}

type MultiReturnRepository interface {
	M(ctx context.Context) (int, string, error)
}

type NamedReturnRepository interface {
	M(ctx context.Context) (result *User, err error)
}

type NoContextRepository interface {
	M(name string) error
}

type EmbeddingRepository interface {
	UserRepository
	Extra(ctx context.Context) error
}
