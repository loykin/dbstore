package elasticsearchadapter

import (
	"context"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"github.com/loykin/dbstore"
)

// Source is a stable entry binding that hands repository backend code a Handle
// instead of the raw *elasticsearch.Client. Value receiver (not pointer) so a
// Source value satisfies dbstore.Runner[Handle].
type Source struct {
	source dbstore.Source[*elasticsearch.Client]
}

func NewSource(name string, exec *dbstore.Executor[*elasticsearch.Client]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Handle] = Source{}

// Name returns the source name this Source was constructed with.
func (s Source) Name() string {
	return s.source.Name()
}

func (s Source) Run(ctx context.Context, fn func(context.Context, Handle) error) error {
	return s.source.Run(ctx, func(ctx context.Context, client *elasticsearch.Client) error {
		return fn(ctx, Handle{client: client})
	})
}
