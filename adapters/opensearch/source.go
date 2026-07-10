package opensearchadapter

import (
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
)

type Source = dbstore.Source[*opensearchapi.Client]

func NewSource(name string, exec *dbstore.Executor[*opensearchapi.Client]) Source {
	return dbstore.NewSource(name, exec)
}
