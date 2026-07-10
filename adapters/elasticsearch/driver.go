package elasticsearchadapter

import (
	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"github.com/loykin/dbstore"
)

type Config = elasticsearch.Config

type Driver struct {
	Config Config
}

func (d Driver) Open(cfg dbstore.SourceConfig) (*elasticsearch.Client, error) {
	clientConfig := d.Config
	if len(clientConfig.Addresses) == 0 && clientConfig.CloudID == "" && cfg.DSN != "" {
		clientConfig.Addresses = []string{cfg.DSN}
	}
	return elasticsearch.NewClient(clientConfig)
}
