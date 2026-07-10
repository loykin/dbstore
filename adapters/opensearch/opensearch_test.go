package opensearchadapter

import (
	"testing"

	"github.com/loykin/dbstore"
)

func TestAdapter_Open(t *testing.T) {
	adapter := New()
	adapter.RegisterDriver("opensearch", Driver{})
	defer adapter.Close()

	if err := adapter.Open("search", dbstore.SourceConfig{
		Driver: "opensearch",
		DSN:    "http://localhost:9200",
	}); err != nil {
		t.Fatal(err)
	}

	source := NewSource("search", adapter.Executor())
	if got := source.Name(); got != "search" {
		t.Fatalf("source name = %q, want search", got)
	}
}
