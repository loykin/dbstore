package opensearchadapter

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
)

// Source hands Template code an Adaptor instead of the raw
// *opensearchapi.Client — see docs/design-codegen.md for why. Value
// receiver (not pointer) so a Source value satisfies dbstore.Runner[Adaptor].
type Source struct {
	source dbstore.Source[*opensearchapi.Client]
}

func NewSource(name string, exec *dbstore.Executor[*opensearchapi.Client]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Adaptor] = Source{}

// Name returns the source name this Source was constructed with.
func (s Source) Name() string {
	return s.source.Name()
}

func (s Source) Run(ctx context.Context, fn func(context.Context, Adaptor) error) error {
	return s.source.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
		return fn(ctx, Adaptor{client: client})
	})
}
