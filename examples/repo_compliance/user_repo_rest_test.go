package main

import (
	"testing"

	"github.com/loykin/dbstore/dbstoretest"
)

var restUserFixture = dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
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
}
