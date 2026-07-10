package elasticsearchadapter

import (
	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"github.com/loykin/dbstore"
)

type Source = dbstore.Source[*elasticsearch.Client]

func NewSource(name string, exec *dbstore.Executor[*elasticsearch.Client]) Source {
	return dbstore.NewSource(name, exec)
}
