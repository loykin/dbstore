//go:build integration

package opensearchadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tcopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"

	"github.com/loykin/dbstore"
)

// TestAdapter_Container proves Driver actually talks to a real OpenSearch
// cluster, not just that it constructs a client without error — the SDK's
// client construction is lazy and doesn't touch the network, so
// TestAdapter_Open (opensearch_test.go) alone can't catch a wiring mistake
// that only breaks on a real request.
func TestAdapter_Container(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcopensearch.Run(ctx, "opensearchproject/opensearch:2.11.1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	address, err := ctr.Address(ctx)
	require.NoError(t, err)

	adapter := New()
	adapter.RegisterDriver("opensearch", Driver{})
	defer adapter.Close()

	require.NoError(t, adapter.Open("search", dbstore.SourceConfig{
		Driver: "opensearch",
		DSN:    address,
	}))

	source := adapter.Source("search")

	err = source.Run(ctx, func(ctx context.Context, a Handle) error {
		if err := a.Index(ctx, "cs_docs", "1", map[string]string{"name": "Alice"}); err != nil {
			return err
		}

		// Handle.Index doesn't force a refresh (that's a per-write
		// performance tradeoff a general-purpose Handle shouldn't hardcode
		// — see handle.go), so poll for OpenSearch's near-real-time
		// refresh instead of asserting the doc is visible immediately.
		var doc map[string]string
		var getErr error
		for i := 0; i < 10; i++ {
			getErr = a.Get(ctx, "cs_docs", "1", &doc)
			if !errors.Is(getErr, dbstore.ErrNotFound) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		return getErr
	})
	require.NoError(t, err)
}
