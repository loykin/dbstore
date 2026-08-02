//go:build integration

package elasticsearchadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tcelasticsearch "github.com/testcontainers/testcontainers-go/modules/elasticsearch"

	"github.com/loykin/dbstore"
)

// TestAdapter_Container proves Driver actually talks to a real Elasticsearch
// cluster, not just that it constructs a client without error — the SDK's
// client construction is lazy and doesn't touch the network, so
// TestAdapter_Open (elasticsearch_test.go) alone can't catch a wiring
// mistake that only breaks on a real request.
func TestAdapter_Container(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcelasticsearch.Run(ctx, "docker.elastic.co/elasticsearch/elasticsearch:8.9.0",
		tcelasticsearch.WithPassword("s3cret"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	adapter := New()
	adapter.RegisterDriver("elasticsearch", Driver{
		Config: Config{
			Username: "elastic",
			Password: ctr.Settings.Password,
			CACert:   ctr.Settings.CACert,
		},
	})
	defer adapter.Close()

	require.NoError(t, adapter.Open("search", dbstore.SourceConfig{
		Driver: "elasticsearch",
		DSN:    ctr.Settings.Address,
	}))

	source := adapter.Source("search")

	err = source.Run(ctx, func(ctx context.Context, a Adaptor) error {
		if err := a.Index(ctx, "cs_docs", "1", map[string]string{"name": "Alice"}); err != nil {
			return err
		}

		// Adaptor.Index doesn't force a refresh (that's a per-write
		// performance tradeoff a general-purpose Adaptor shouldn't hardcode
		// — see adaptor.go), so poll for Elasticsearch's near-real-time
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
