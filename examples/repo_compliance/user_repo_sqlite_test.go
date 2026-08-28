package main

import (
	"context"
	"testing"

	"github.com/loykin/dbstore/dbstoretest"
)

var sqliteUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
	Name: "SQLite",
	New: func(t *testing.T) UserRepository {
		repo, cleanup, err := setupSQLite(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return repo
	},
	Caps: userRepoCapabilities{AtomicBatch: true},
}
