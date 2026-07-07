package main

import (
	"context"
	"testing"
)

func TestUserRepository(t *testing.T) {
	ctx := context.Background()
	repo, cleanup, err := setupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if err := repo.Create(ctx, "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateBatch(ctx, []string{"Bob", "Carol"}); err != nil {
		t.Fatal(err)
	}

	user, err := repo.FindByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "Alice" {
		t.Fatalf("FindByID name = %q, want Alice", user.Name)
	}

	users, err := repo.FindAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("FindAll len = %d, want 3", len(users))
	}
}
