package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/loykin/dbstore"
)

type ListSourcesInput struct{}

type ListSourcesOutput struct {
	Sources []dbstore.SourceInfo `json:"sources"`
}

type SourceInput struct {
	Source string `json:"source" jsonschema:"registered dbstore source name"`
}

type PingOutput struct {
	Source      string  `json:"source"`
	DurationMS  float64 `json:"durationMs"`
	Driver      string  `json:"driver"`
	Connections int     `json:"openConnections"`
	InUse       int     `json:"inUse"`
	Idle        int     `json:"idle"`
}

type ListTablesOutput struct {
	Source string      `json:"source"`
	Tables []TableInfo `json:"tables"`
}

type DescribeTableInput struct {
	Source string `json:"source" jsonschema:"registered dbstore source name"`
	Table  string `json:"table" jsonschema:"table name to inspect"`
}

type DescribeTableOutput struct {
	Source  string       `json:"source"`
	Table   string       `json:"table"`
	Columns []ColumnInfo `json:"columns"`
}

type QueryInput struct {
	Source    string `json:"source" jsonschema:"registered dbstore source name"`
	Query     string `json:"query" jsonschema:"single read-only SELECT statement"`
	Args      []any  `json:"args,omitempty" jsonschema:"positional query arguments"`
	MaxRows   int    `json:"maxRows,omitempty" jsonschema:"maximum rows to return"`
	MaxBytes  int    `json:"maxBytes,omitempty" jsonschema:"maximum encoded result bytes to return"`
	TimeoutMS int    `json:"timeoutMs,omitempty" jsonschema:"query timeout in milliseconds, at most 30000"`
}

type QueryOutput struct {
	Source    string   `json:"source"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Truncated bool     `json:"truncated"`
}

type RegisterSourceInput struct {
	Name      string `json:"name" jsonschema:"new dbstore source name"`
	ConfigRef string `json:"configRef" jsonschema:"opaque reference resolved by the server; never a raw DSN"`
}

type SourceMutationOutput struct {
	Name string `json:"name"`
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed}
}

func mutatingAnnotations(destructive bool) *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, OpenWorldHint: &closed}
}

func queryAnnotations() *mcp.ToolAnnotations {
	closed := false
	potentiallyDestructive := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &potentiallyDestructive,
		OpenWorldHint:   &closed,
	}
}

func (s *Server) addTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_list_sources",
		Description: "List registered dbstore SQL sources using redacted metadata. DSNs and credentials are never returned.",
		Annotations: readOnlyAnnotations(),
	}, s.listSources)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_ping",
		Description: "Ping one registered SQL source and return connection-pool statistics.",
		Annotations: readOnlyAnnotations(),
	}, s.ping)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_list_tables",
		Description: "List user-visible tables for one registered SQLite, PostgreSQL, or MySQL source.",
		Annotations: readOnlyAnnotations(),
	}, s.listTables)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_describe_table",
		Description: "Describe columns for one table without exposing connection credentials.",
		Annotations: readOnlyAnnotations(),
	}, s.describeTable)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_query",
		Description: "Run one bounded SELECT statement through dbstore.Executor. Disabled by default because SELECT functions may have side effects; database grants are the security boundary.",
		Annotations: queryAnnotations(),
	}, s.query)
	if s.manager != nil {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "db_remove_source",
			Description: "Remove a registered source after its in-flight dbstore operations finish. Disabled by the default policy.",
			Annotations: mutatingAnnotations(true),
		}, s.removeSource)
	}
	if s.manager != nil && s.sourceResolver != nil {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "db_register_source",
			Description: "Register a source from an opaque server-side configuration reference. Raw DSNs are not accepted.",
			Annotations: mutatingAnnotations(false),
		}, s.registerSource)
	}
}

func (s *Server) listSources(ctx context.Context, _ *mcp.CallToolRequest, _ ListSourcesInput) (*mcp.CallToolResult, ListSourcesOutput, error) {
	if err := s.policy.Authorize(ctx, OperationListSources, ""); err != nil {
		return nil, ListSourcesOutput{}, err
	}
	return nil, ListSourcesOutput{Sources: s.store.Sources()}, nil
}

func (s *Server) ping(ctx context.Context, _ *mcp.CallToolRequest, in SourceInput) (*mcp.CallToolResult, PingOutput, error) {
	if err := requireSource(in.Source); err != nil {
		return nil, PingOutput{}, err
	}
	if err := s.policy.Authorize(ctx, OperationPing, in.Source); err != nil {
		return nil, PingOutput{}, err
	}
	start := time.Now()
	output := PingOutput{Source: in.Source}
	err := s.store.Executor().Run(ctx, in.Source, func(ctx context.Context, db *sqlx.DB) error {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		stats := db.Stats()
		output.Driver = db.DriverName()
		output.Connections = stats.OpenConnections
		output.InUse = stats.InUse
		output.Idle = stats.Idle
		return nil
	})
	output.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return nil, output, err
}

func (s *Server) removeSource(ctx context.Context, _ *mcp.CallToolRequest, in SourceInput) (*mcp.CallToolResult, SourceMutationOutput, error) {
	if err := requireSource(in.Source); err != nil {
		return nil, SourceMutationOutput{}, err
	}
	if err := s.policy.Authorize(ctx, OperationRemove, in.Source); err != nil {
		return nil, SourceMutationOutput{}, err
	}
	if err := s.manager.Remove(in.Source); err != nil {
		return nil, SourceMutationOutput{}, err
	}
	return nil, SourceMutationOutput{Name: in.Source}, nil
}

func (s *Server) registerSource(ctx context.Context, _ *mcp.CallToolRequest, in RegisterSourceInput) (*mcp.CallToolResult, SourceMutationOutput, error) {
	if err := requireSource(in.Name); err != nil {
		return nil, SourceMutationOutput{}, err
	}
	if in.ConfigRef == "" {
		return nil, SourceMutationOutput{}, fmt.Errorf("mcpserver: configRef is required")
	}
	if err := s.policy.Authorize(ctx, OperationRegister, in.Name); err != nil {
		return nil, SourceMutationOutput{}, err
	}
	cfg, err := s.sourceResolver.ResolveSource(ctx, in.ConfigRef)
	if err != nil {
		return nil, SourceMutationOutput{}, fmt.Errorf("mcpserver: source configuration could not be resolved")
	}
	if err := s.manager.Open(in.Name, cfg); err != nil {
		return nil, SourceMutationOutput{}, fmt.Errorf("mcpserver: source registration failed")
	}
	return nil, SourceMutationOutput{Name: in.Name}, nil
}

func requireSource(source string) error {
	if source == "" {
		return fmt.Errorf("mcpserver: source is required")
	}
	return nil
}
