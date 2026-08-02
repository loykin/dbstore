// Generated skeleton by dbstore-gen for backend "rest" —
// created once, never overwritten. Fill in the TODO bodies; the signatures
// already match UserRepoTemplate[restadapter.Adaptor], so a
// future UserRepository method addition shows up here as a compile
// error (see the "var _ ..." assertion in the _gen.go file), not silently.

package main

import (
	"context"
	"fmt"

	restadapter "github.com/loykin/dbstore/adapters/rest"
)

type RestUserTemplate struct{}

func (RestUserTemplate) Create(ctx context.Context, a restadapter.Adaptor, name string) error {
	return a.Post(ctx, "/users", User{Name: name})
}

// CreateBatch is best-effort sequential — this API has no batch endpoint or
// transaction concept, so restadapter.Adaptor has no WithTx to reach for.
// See dbstoretest.Capabilities{AtomicBatch: false} (the zero value) in
// main_test.go.
func (RestUserTemplate) CreateBatch(ctx context.Context, a restadapter.Adaptor, names []string) error {
	for _, name := range names {
		if err := a.Post(ctx, "/users", User{Name: name}); err != nil {
			return err
		}
	}
	return nil
}

func (RestUserTemplate) FindAll(ctx context.Context, a restadapter.Adaptor) ([]User, error) {
	var users []User
	err := a.Get(ctx, "/users", &users)
	return users, err
}

func (RestUserTemplate) FindByID(ctx context.Context, a restadapter.Adaptor, id int) (*User, error) {
	var u User
	err := a.Get(ctx, fmt.Sprintf("/users/%d", id), &u)
	return &u, err
}
