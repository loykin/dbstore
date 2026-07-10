package elasticsearchadapter

import (
	"testing"

	"github.com/loykin/dbstore"
)

func TestAdapter_Open(t *testing.T) {
	adapter := New()
	adapter.RegisterDriver("elasticsearch", Driver{})
	defer adapter.Close()

	if err := adapter.Open("search", dbstore.SourceConfig{
		Driver: "elasticsearch",
		DSN:    "http://localhost:9200",
	}); err != nil {
		t.Fatal(err)
	}

	source := NewSource("search", adapter.Executor())
	if got := source.Name(); got != "search" {
		t.Fatalf("source name = %q, want search", got)
	}
}
