package main

import (
	"context"
	"testing"
)

func TestDocumentRepository(t *testing.T) {
	ctx := context.Background()
	server := newFakeOpenSearchServer()
	defer server.Close()

	repo, cleanup, err := setupStore(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if err := repo.Create(ctx, "1", "Alice"); err != nil {
		t.Fatal(err)
	}

	doc, err := repo.FindByID(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID != "1" {
		t.Fatalf("ID = %q, want 1", doc.ID)
	}
	if doc.Name != "Alice" {
		t.Fatalf("Name = %q, want Alice", doc.Name)
	}
}
