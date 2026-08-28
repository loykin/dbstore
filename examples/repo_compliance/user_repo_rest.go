// Generated skeleton by dbstore-gen for backend "rest" —
// created once, never overwritten. Fill in the TODO bodies; the signatures
// already match UserRepoBackend[restadapter.Handle], so a
// future UserRepository method addition shows up here as a compile
// error (see the "var _ ..." assertion in the _gen.go file), not silently.

package main

import (
	"context"
	"fmt"

	restadapter "github.com/loykin/dbstore/adapters/rest"
)

type RestUserBackend struct{}

func NewRestUserRepository(source restadapter.Source) UserRepository {
	return NewUserRepo[restadapter.Handle](RestUserBackend{}, source)
}

func (RestUserBackend) Create(ctx context.Context, h restadapter.Handle, name string) error {
	return h.Post(ctx, "/users", User{Name: name})
}

// CreateBatch is best-effort sequential — this API has no batch endpoint or
// transaction concept, so restadapter.Handle has no WithTx to reach for.
// See userRepoCapabilities{AtomicBatch: false} (the zero value) in
// user_repo_rest_test.go.
func (RestUserBackend) CreateBatch(ctx context.Context, h restadapter.Handle, names []string) error {
	for _, name := range names {
		if err := h.Post(ctx, "/users", User{Name: name}); err != nil {
			return err
		}
	}
	return nil
}

func (RestUserBackend) FindAll(ctx context.Context, h restadapter.Handle) ([]User, error) {
	var users []User
	err := h.Get(ctx, "/users", &users)
	return users, err
}

func (RestUserBackend) FindByID(ctx context.Context, h restadapter.Handle, id int) (*User, error) {
	var u User
	err := h.Get(ctx, fmt.Sprintf("/users/%d", id), &u)
	return &u, err
}
