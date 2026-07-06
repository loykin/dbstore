//go:build integration

// This file is research, not product code: it exists to validate (or
// invalidate) the `Pool[T Closer]` design proposed in docs/requirements.md
// against the real github.com/opensearch-project/opensearch-go/v4 client,
// per the doc's own "검증 계획" (don't design Pool[T] abstractly — build it
// against a real backend's actual requirements first).
package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/stretchr/testify/require"
)

// TestOpenSearch_ClientDoesNotImplementCloser is the key finding that shaped
// pool.go: unlike *sqlx.DB, *opensearchapi.Client has no Close method. The
// client is a thin wrapper over net/http's transport, which manages (and
// reuses/expires) its own idle connections; there is nothing for a caller to
// explicitly close. This is why Pool[T] treats Closer as an optional
// capability (checked via type assertion in closeClient), not a required
// type constraint — Pool[*opensearchapi.Client] would be impossible
// otherwise.
func TestOpenSearch_ClientDoesNotImplementCloser(t *testing.T) {
	closerType := reflect.TypeOf((*Closer)(nil)).Elem()
	clientType := reflect.TypeOf((*opensearchapi.Client)(nil))

	require.False(t, clientType.Implements(closerType), "expected *opensearchapi.Client to NOT implement Closer — "+
		"if this now fails, the client gained a Close() method upstream and pool.go's optional-Closer design note should be revisited")
}

// TestOpenSearch_DocumentRoundTrip_And_NoTransactions validates the second
// half of the design note's concern: operation shape. There is no
// BeginTxx/Commit/Rollback equivalent — Create/FindByID go through
// openSearchDocRepo (opensearch_repo_test.go), the same generic
// Pool[T]/Executor[T]/BaseRepo[T] stack real callers would use, rather than
// a second hand-rolled client, so this test can't silently drift from what
// DocRepository actually does.
func TestOpenSearch_DocumentRoundTrip_And_NoTransactions(t *testing.T) {
	ctx := context.Background()
	addr := startOpenSearchContainer(t)

	registry := NewDriverRegistry[*opensearchapi.Client]()
	registry.Register("opensearch", &openSearchDriver{})

	pool := NewPool(registry)
	t.Cleanup(pool.RemoveAll)
	require.NoError(t, pool.Register("primary", DriverConfig{
		Driver: "opensearch",
		DSN:    addr,
	}))

	exec := NewExecutor(pool)
	repo := newOpenSearchDocRepo(exec, "cs_docs")

	require.NoError(t, repo.Create(ctx, "1", "Alice"))

	// A just-created document is a read-your-writes Get, not a search — it
	// succeeds immediately even without a refresh. This is the "eventually
	// consistent for search, but not for direct Get" behavior requirements.md
	// flags as backend-specific and explicitly not something a shared
	// interface should paper over.
	doc, err := repo.FindByID(ctx, "1")
	require.NoError(t, err)
	require.Equal(t, "Alice", doc.Name)

	// Document.Create is a single HTTP call with no Commit/Rollback step.
	// Multi-document writes go through the Bulk API instead, which reports
	// success/failure per item rather than atomically rolling back —
	// confirming requirements.md's point that RunTx cannot be generalized
	// across backend operation shapes; it stays a *sqlx.Tx-specific concept.
}
