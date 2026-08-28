// Fixture skeleton created by dbstore-gen for backend "sqlite".
// This file is application-owned and is never overwritten. Replace the panic
// with backend setup and register cleanup with t.Cleanup.

package main

import (
	"context"
	"testing"

	"github.com/loykin/dbstore/dbstoretest"
)

var sqliteUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "SQLite",
	New: func(t *testing.T) UserRepository {
		t.Helper()
		repo, cleanup, err := setupStore(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return repo
	},
}
