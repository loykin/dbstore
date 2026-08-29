package restadapter

import (
	"context"

	"github.com/loykin/dbstore"
)

// Source is a stable entry binding that hands repository backend code a Handle
// instead of the raw *Client. Value receiver (not pointer) so a Source value
// satisfies dbstore.Runner[Handle].
type Source struct {
	source dbstore.Source[*Client]
}

func NewSource(name string, exec *dbstore.Executor[*Client]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

var _ dbstore.Runner[Handle] = Source{}

func (s Source) Run(ctx context.Context, fn func(context.Context, Handle) error) error {
	return s.source.Run(ctx, func(ctx context.Context, c *Client) error {
		return fn(ctx, Handle{c: c})
	})
}
