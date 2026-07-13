// Package mcpserver exposes dbstore-managed SQL sources through the Model
// Context Protocol. It is both embeddable in an existing service (sharing that
// service's Adapter and connection pools) and used by cmd/dbstore-mcp.
package mcpserver

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/loykin/dbstore"
)

const modulePath = "github.com/loykin/dbstore"

// SQLStore is the dbstore capability set used by Server. sqlxadapter.Adapter
// satisfies it; applications may supply a wrapper to enforce additional
// lifecycle rules.
type SQLStore interface {
	Sources() []dbstore.SourceInfo
	Executor() *dbstore.Executor[*sqlx.DB]
}

// SourceManager is the optional lifecycle capability used by management
// tools. Keeping it separate lets read-only embeddings expose a narrower API.
type SourceManager interface {
	Open(name string, cfg dbstore.SourceConfig) error
	Remove(name string) error
}

// SourceConfigResolver turns an opaque credential/configuration reference into
// a SourceConfig. MCP callers never submit a raw DSN through the protocol.
type SourceConfigResolver interface {
	ResolveSource(ctx context.Context, ref string) (dbstore.SourceConfig, error)
}

// SourceConfigResolverFunc adapts a function to SourceConfigResolver.
type SourceConfigResolverFunc func(context.Context, string) (dbstore.SourceConfig, error)

func (f SourceConfigResolverFunc) ResolveSource(ctx context.Context, ref string) (dbstore.SourceConfig, error) {
	return f(ctx, ref)
}

type Options struct {
	Store            SQLStore
	EnableManagement bool
	Policy           Policy
	SourceResolver   SourceConfigResolver
	Name             string
	Version          string
	DefaultMaxRows   int
	MaximumMaxRows   int
	DefaultMaxBytes  int
	MaximumMaxBytes  int
}

// Server is an MCP server backed by a caller-owned SQLStore. Closing Server
// never closes or removes sources; the creator of the Store retains ownership.
type Server struct {
	store           SQLStore
	manager         SourceManager
	policy          Policy
	sourceResolver  SourceConfigResolver
	mcp             *mcp.Server
	defaultMaxRows  int
	maximumMaxRows  int
	defaultMaxBytes int
	maximumMaxBytes int
}

func New(opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("mcpserver: SQL store is required")
	}
	if opts.Policy == nil {
		opts.Policy = InspectionPolicy{}
	}
	var manager SourceManager
	if opts.EnableManagement {
		var ok bool
		manager, ok = opts.Store.(SourceManager)
		if !ok {
			return nil, fmt.Errorf("mcpserver: management requires Store to implement SourceManager")
		}
	} else if opts.SourceResolver != nil {
		return nil, fmt.Errorf("mcpserver: source resolver requires management to be enabled")
	}
	if opts.Name == "" {
		opts.Name = "dbstore-mcp"
	}
	if opts.Version == "" {
		opts.Version = implementationVersion()
	}
	if opts.DefaultMaxRows <= 0 {
		opts.DefaultMaxRows = 100
	}
	if opts.MaximumMaxRows <= 0 {
		opts.MaximumMaxRows = 1000
	}
	if opts.DefaultMaxRows > opts.MaximumMaxRows {
		return nil, fmt.Errorf("mcpserver: default max rows cannot exceed maximum max rows")
	}
	if opts.DefaultMaxBytes <= 0 {
		opts.DefaultMaxBytes = 256 << 10
	}
	if opts.MaximumMaxBytes <= 0 {
		opts.MaximumMaxBytes = 4 << 20
	}
	if opts.DefaultMaxBytes > opts.MaximumMaxBytes {
		return nil, fmt.Errorf("mcpserver: default max bytes cannot exceed maximum max bytes")
	}

	s := &Server{
		store:          opts.Store,
		manager:        manager,
		policy:         opts.Policy,
		sourceResolver: opts.SourceResolver,
		mcp: mcp.NewServer(&mcp.Implementation{
			Name:    opts.Name,
			Version: opts.Version,
		}, nil),
		defaultMaxRows:  opts.DefaultMaxRows,
		maximumMaxRows:  opts.MaximumMaxRows,
		defaultMaxBytes: opts.DefaultMaxBytes,
		maximumMaxBytes: opts.MaximumMaxBytes,
	}
	s.addTools()
	return s, nil
}

func implementationVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if info.Main.Path == modulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, dependency := range info.Deps {
		if dependency.Path == modulePath && dependency.Version != "" {
			return dependency.Version
		}
	}
	return "devel"
}

// MCP returns the underlying SDK server for applications that need to attach
// middleware, resources, or a transport not wrapped by this package.
func (s *Server) MCP() *mcp.Server {
	return s.mcp
}

// Run serves one MCP transport until the client disconnects or ctx is canceled.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	return s.mcp.Run(ctx, transport)
}

// ServeStdio serves MCP over stdin/stdout.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.Run(ctx, &mcp.StdioTransport{})
}
