package restadapter

import (
	"context"

	"github.com/loykin/dbstore"
)

type Source struct {
	source dbstore.Source[*Client]
}

func NewSource(name string, exec *dbstore.Executor[*Client]) Source {
	return Source{source: dbstore.NewSource(name, exec)}
}

func (s *Source) Run(ctx context.Context, fn func(context.Context, *Client) error) error {
	return s.source.Run(ctx, fn)
}
