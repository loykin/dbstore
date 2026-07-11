package main

import (
	"context"
	"testing"
)

// runUserRepoComplianceSuite asserts the behavior every UserRepository
// implementation must share. It only calls interface methods — never a
// backend-specific type — so the same function runs unchanged against the
// SQLite-backed and REST-backed implementations below. Transactional
// rollback isn't asserted here because it isn't part of the shared
// contract: not every backend can guarantee it (compare
// internal/store/repo_compliance_test.go, which does assert it, but only
// across SQL backends where it's a fair requirement).
func runUserRepoComplianceSuite(t *testing.T, newRepo func(t *testing.T) UserRepository) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_and_FindByID", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.Create(ctx, "Alice"); err != nil {
			t.Fatal(err)
		}
		users, err := repo.FindAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 {
			t.Fatalf("FindAll len = %d, want 1", len(users))
		}
		u, err := repo.FindByID(ctx, users[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if u.Name != "Alice" {
			t.Fatalf("Name = %q, want Alice", u.Name)
		}
	})

	t.Run("FindAll_PreservesInsertOrder", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.Create(ctx, "Alice"); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctx, "Bob"); err != nil {
			t.Fatal(err)
		}
		users, err := repo.FindAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 2 || users[0].Name != "Alice" || users[1].Name != "Bob" {
			t.Fatalf("FindAll = %+v, want [Alice Bob]", users)
		}
	})

	t.Run("FindByID_NotFound", func(t *testing.T) {
		repo := newRepo(t)
		if _, err := repo.FindByID(ctx, 999); err == nil {
			t.Fatal("want error for missing id, got nil")
		}
	})

	t.Run("CreateBatch_Commit", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.CreateBatch(ctx, []string{"Alice", "Bob", "Carol"}); err != nil {
			t.Fatal(err)
		}
		users, err := repo.FindAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 3 {
			t.Fatalf("FindAll len = %d, want 3", len(users))
		}
	})
}

func TestUserRepoCompliance_SQLite(t *testing.T) {
	runUserRepoComplianceSuite(t, func(t *testing.T) UserRepository {
		repo, cleanup, err := setupSQLite(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return repo
	})
}

func TestUserRepoCompliance_REST(t *testing.T) {
	runUserRepoComplianceSuite(t, func(t *testing.T) UserRepository {
		server := newFakeUsersServer()
		t.Cleanup(server.Close)

		repo, cleanup, err := setupREST(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return repo
	})
}
