//go:build integration

package opensearchadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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

	source := NewSource("search", adapter.Executor())

	err = source.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
		body, err := json.Marshal(map[string]string{"name": "Alice"})
		if err != nil {
			return err
		}
		if _, err := client.Index(ctx, opensearchapi.IndexReq{
			Index:      "cs_docs",
			DocumentID: "1",
			Body:       bytes.NewReader(body),
			Params:     opensearchapi.IndexParams{Refresh: "true"},
		}); err != nil {
			return err
		}

		resp, err := client.Document.Get(ctx, opensearchapi.DocumentGetReq{
			Index:      "cs_docs",
			DocumentID: "1",
		})
		if err != nil {
			return err
		}
		if !resp.Found {
			return fmt.Errorf("document not found after index")
		}
		return nil
	})
	require.NoError(t, err)
}
