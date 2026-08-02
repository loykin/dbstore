package restadapter

import (
	"context"

	"github.com/loykin/dbstore"
)

// Source hands Template code an Adaptor instead of the raw *Client. Value
// receiver (not pointer) so a Source value satisfies dbstore.Runner[Adaptor].
type Source struct {
	source dbstore.Source[*Client]
}

func NewSource(name string, exec *dbstore.Executor[*Client]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Adaptor] = Source{}

func (s Source) Run(ctx context.Context, fn func(context.Context, Adaptor) error) error {
	return s.source.Run(ctx, func(ctx context.Context, c *Client) error {
		return fn(ctx, Adaptor{c: c})
	})
}
