// Compliance suite skeleton created by dbstore-gen.
// This file is application-owned and is never overwritten. Write shared
// assertions below using only UserRepository methods.

package main

import (
	"context"
	"testing"
)

// userRepoCapabilities declares optional guarantees specific to
// UserRepository. Add fields only for behavior that some configured
// backends can guarantee and others cannot.
type userRepoCapabilities struct{}

// runUserRepoComplianceSuite asserts the behavior every
// UserRepository implementation must share. caps gates assertions not
// every backend can honor (e.g. multi-op atomicity).
func runUserRepoComplianceSuite(t *testing.T, newRepo func(t *testing.T) UserRepository, caps userRepoCapabilities) {
	t.Helper()
	_ = caps
	ctx := context.Background()

	t.Run("Create_batch_and_find", func(t *testing.T) {
		repo := newRepo(t)
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
	})

	t.Run("FindByID_not_found", func(t *testing.T) {
		user, err := newRepo(t).FindByID(ctx, 999)
		if err != nil {
			t.Fatal(err)
		}
		if user != nil {
			t.Fatalf("FindByID = %+v, want nil", user)
		}
	})
}
