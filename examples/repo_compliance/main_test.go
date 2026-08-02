package main

import (
	"context"
	"testing"

	"github.com/loykin/dbstore/dbstoretest"
)

// runUserRepoComplianceSuite asserts the behavior every UserRepository
// implementation must share. It only calls interface methods — never a
// backend-specific type — so the same function runs unchanged against the
// SQLite-backed and REST-backed implementations below. caps.AtomicBatch
// gates CreateBatch_Rollback: REST has no transaction concept (its Adaptor
// has no WithTx at all — see user_repo_rest.go), so its fixture leaves
// AtomicBatch false and the suite skips that assertion for it instead of
// either failing REST or silently never checking SQLite's real guarantee.
func runUserRepoComplianceSuite(t *testing.T, newRepo func(t *testing.T) UserRepository, caps dbstoretest.Capabilities) {
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
		u, err := repo.FindByID(ctx, 999)
		if err != nil {
			t.Fatalf("want (nil, nil) for missing id, got err = %v", err)
		}
		if u != nil {
			t.Fatalf("want nil user for missing id, got %+v", u)
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

	if caps.AtomicBatch {
		t.Run("CreateBatch_Rollback", func(t *testing.T) {
			repo := newRepo(t)
			// "Alice" twice violates the UNIQUE constraint on name (see
			// setupSQLite) partway through the batch — proving the whole
			// batch rolled back, not just the failing insert.
			if err := repo.CreateBatch(ctx, []string{"Alice", "Alice"}); err == nil {
				t.Fatal("want error for duplicate name, got nil")
			}
			users, err := repo.FindAll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(users) != 0 {
				t.Fatalf("FindAll len = %d, want 0 after rollback", len(users))
			}
		})
	}
}

// TestUserRepoCompliance runs the one compliance suite above against both
// backends via dbstoretest.RunComplianceSuite, instead of a hand-written
// loop — see the root README's "Why" for what this pattern is for.
func TestUserRepoCompliance(t *testing.T) {
	dbstoretest.RunComplianceSuite(t, []dbstoretest.Fixture[UserRepository]{
		{
			Name: "SQLite",
			New: func(t *testing.T) UserRepository {
				repo, cleanup, err := setupSQLite(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanup)
				return repo
			},
			Caps: dbstoretest.Capabilities{AtomicBatch: true},
		},
		{
			Name: "REST",
			New: func(t *testing.T) UserRepository {
				server := newFakeUsersServer()
				t.Cleanup(server.Close)

				repo, cleanup, err := setupREST(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanup)
				return repo
			},
			// Caps left zero-value: REST has no atomic CreateBatch.
		},
	}, runUserRepoComplianceSuite)
}
