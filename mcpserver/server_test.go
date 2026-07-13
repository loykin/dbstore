package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
)

func newTestStore(t *testing.T) *sqlxadapter.Adapter {
	t.Helper()
	store := sqlxadapter.New()
	store.RegisterDefaultDrivers()
	require.NoError(t, store.Open("primary", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite,
		DSN:    "file:mcpserver_test?mode=memory&cache=shared",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}))
	t.Cleanup(store.Close)
	require.NoError(t, store.Executor().Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		if _, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES ('Ada'), ('Grace'), ('Linus')`)
		return err
	}))
	return store
}

func connectTestClient(t *testing.T, server *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "dbstore-mcp-test", Version: "v0.0.1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, clientSession.Close())
		require.NoError(t, serverSession.Wait())
	})
	return clientSession
}

func callTool[Out any](t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) (Out, *mcp.CallToolResult) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	var output Out
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &output))
	}
	return output, result
}

func TestServer_InspectionAndQueryOverMCPTransport(t *testing.T) {
	store := newTestStore(t)
	server, err := New(Options{Store: store, Policy: QueryPolicy{}, DefaultMaxRows: 2})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	sources, result := callTool[ListSourcesOutput](t, session, "db_list_sources", nil)
	require.False(t, result.IsError)
	require.Len(t, sources.Sources, 1)
	assert.Equal(t, "primary", sources.Sources[0].Name)
	assert.Equal(t, "sqlite", sources.Sources[0].Driver)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "mcpserver_test", "MCP output must not expose the DSN")

	ping, result := callTool[PingOutput](t, session, "db_ping", map[string]any{"source": "primary"})
	require.False(t, result.IsError)
	assert.Equal(t, "sqlite", ping.Driver)
	assert.GreaterOrEqual(t, ping.Connections, 1)

	tables, result := callTool[ListTablesOutput](t, session, "db_list_tables", map[string]any{"source": "primary"})
	require.False(t, result.IsError)
	require.Len(t, tables.Tables, 1)
	assert.Equal(t, "users", tables.Tables[0].Name)

	description, result := callTool[DescribeTableOutput](t, session, "db_describe_table", map[string]any{
		"source": "primary",
		"table":  "users",
	})
	require.False(t, result.IsError)
	require.Len(t, description.Columns, 2)
	assert.Equal(t, "id", description.Columns[0].Name)
	assert.True(t, description.Columns[0].PrimaryKey)

	query, result := callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "SELECT id, name FROM users ORDER BY id",
	})
	require.False(t, result.IsError)
	assert.Equal(t, 2, query.RowCount)
	assert.True(t, query.Truncated)
	assert.Equal(t, "Ada", query.Rows[0][1])

	duplicateColumns, result := callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "SELECT id AS value, name AS value FROM users WHERE id = 1",
	})
	require.False(t, result.IsError)
	assert.Equal(t, []string{"value", "value"}, duplicateColumns.Columns)
	assert.Equal(t, float64(1), duplicateColumns.Rows[0][0])
	assert.Equal(t, "Ada", duplicateColumns.Rows[0][1])
}

func TestServer_QueryPolicyRejectsWritesAndManagement(t *testing.T) {
	store := newTestStore(t)
	server, err := New(Options{Store: store, EnableManagement: true, Policy: QueryPolicy{}})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	_, result := callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "DELETE FROM users",
	})
	require.True(t, result.IsError)
	_, result = callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "SELECT 1; DELETE FROM users",
	})
	require.True(t, result.IsError)

	_, result = callTool[SourceMutationOutput](t, session, "db_remove_source", map[string]any{"source": "primary"})
	require.True(t, result.IsError)
	require.Len(t, store.Sources(), 1, "denied management must not mutate the store")
}

func TestServer_DefaultPolicyDeniesQuery(t *testing.T) {
	store := newTestStore(t)
	server, err := New(Options{Store: store})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	_, result := callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "SELECT 1",
	})
	require.True(t, result.IsError)
}

func TestServer_ManagementUsesOpaqueConfigReference(t *testing.T) {
	store := sqlxadapter.New()
	store.RegisterDefaultDrivers()
	t.Cleanup(store.Close)
	resolverCalls := 0
	server, err := New(Options{
		Store:            store,
		EnableManagement: true,
		Policy:           AllowAllPolicy{},
		SourceResolver: SourceConfigResolverFunc(func(_ context.Context, ref string) (dbstore.SourceConfig, error) {
			resolverCalls++
			assert.Equal(t, "tenant-a", ref)
			return dbstore.SourceConfig{
				Driver: sqlxadapter.DriverSQLite,
				DSN:    "file:mcpserver_management?mode=memory&cache=shared",
			}, nil
		}),
	})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	registered, result := callTool[SourceMutationOutput](t, session, "db_register_source", map[string]any{
		"name":      "tenant",
		"configRef": "tenant-a",
	})
	require.False(t, result.IsError)
	assert.Equal(t, "tenant", registered.Name)
	assert.Equal(t, 1, resolverCalls)
	require.Len(t, store.Sources(), 1)

	removed, result := callTool[SourceMutationOutput](t, session, "db_remove_source", map[string]any{"source": "tenant"})
	require.False(t, result.IsError)
	assert.Equal(t, "tenant", removed.Name)
	assert.Empty(t, store.Sources())
}

func TestServer_ResolverErrorsCannotLeakCredentialsToMCP(t *testing.T) {
	store := sqlxadapter.New()
	store.RegisterDefaultDrivers()
	t.Cleanup(store.Close)
	const secret = "password=do-not-leak"
	server, err := New(Options{
		Store:            store,
		EnableManagement: true,
		Policy:           AllowAllPolicy{},
		SourceResolver: SourceConfigResolverFunc(func(context.Context, string) (dbstore.SourceConfig, error) {
			return dbstore.SourceConfig{}, errors.New(secret)
		}),
	})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	_, result := callTool[SourceMutationOutput](t, session, "db_register_source", map[string]any{
		"name":      "tenant",
		"configRef": "tenant-a",
	})
	require.True(t, result.IsError)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), secret)
}

func TestServer_QueryResponseByteLimit(t *testing.T) {
	store := newTestStore(t)
	server, err := New(Options{
		Store:           store,
		Policy:          QueryPolicy{},
		DefaultMaxBytes: 256,
		MaximumMaxBytes: 512,
	})
	require.NoError(t, err)
	session := connectTestClient(t, server)

	_, result := callTool[QueryOutput](t, session, "db_query", map[string]any{
		"source": "primary",
		"query":  "SELECT printf('%01000d', 1) AS oversized",
	})
	require.True(t, result.IsError)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "maxBytes")
}

type inspectionStoreView struct {
	store *sqlxadapter.Adapter
}

func (v inspectionStoreView) Sources() []dbstore.SourceInfo {
	return v.store.Sources()
}

func (v inspectionStoreView) Executor() *dbstore.Executor[*sqlx.DB] {
	return v.store.Executor()
}

func TestServer_ManagementMustBeImplementedByStore(t *testing.T) {
	store := newTestStore(t)
	_, err := New(Options{
		Store:            inspectionStoreView{store: store},
		EnableManagement: true,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "Store to implement SourceManager")
}
