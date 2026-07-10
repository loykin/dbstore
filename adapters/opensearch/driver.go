package opensearchadapter

import (
	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
)

type Config = opensearch.Config

type Driver struct {
	Config Config
}

func (d Driver) Open(cfg dbstore.SourceConfig) (*opensearchapi.Client, error) {
	clientConfig := d.Config
	if len(clientConfig.Addresses) == 0 && cfg.DSN != "" {
		clientConfig.Addresses = []string{cfg.DSN}
	}
	return opensearchapi.NewClient(opensearchapi.Config{
		Client: clientConfig,
	})
}
