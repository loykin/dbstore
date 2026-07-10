//go:build integration

package elasticsearchadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
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

	source := NewSource("search", adapter.Executor())

	err = source.Run(ctx, func(ctx context.Context, client *elasticsearch.Client) error {
		body, err := json.Marshal(map[string]string{"name": "Alice"})
		if err != nil {
			return err
		}
		indexResp, err := client.Index("cs_docs", bytes.NewReader(body),
			client.Index.WithDocumentID("1"),
			client.Index.WithContext(ctx),
			client.Index.WithRefresh("true"),
		)
		if err != nil {
			return err
		}
		defer func() { _ = indexResp.Body.Close() }()
		if indexResp.IsError() {
			return fmt.Errorf("index failed: %s", indexResp.Status())
		}

		getResp, err := client.Get("cs_docs", "1", client.Get.WithContext(ctx))
		if err != nil {
			return err
		}
		defer func() { _ = getResp.Body.Close() }()
		if getResp.IsError() {
			return fmt.Errorf("get failed: %s", getResp.Status())
		}
		return nil
	})
	require.NoError(t, err)
}
