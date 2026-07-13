package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loykin/dbstore/mcpserver"
)

func TestEnvironmentSourceResolver(t *testing.T) {
	const value = `{"driver":"sqlite","dsn":"file:test?mode=memory"}`
	t.Setenv("DBSTORE_MCP_SOURCE_ANALYTICS", value)

	cfg, err := (environmentSourceResolver{}).ResolveSource(context.Background(), "analytics")
	require.NoError(t, err)
	assert.Equal(t, "sqlite", cfg.Driver)
	assert.Equal(t, "file:test?mode=memory", cfg.DSN)
}

func TestEnvironmentSourceResolverDoesNotEchoSecretValueOnInvalidJSON(t *testing.T) {
	const secret = "super-secret-password"
	t.Setenv("DBSTORE_MCP_SOURCE_BAD", `{"driver":"postgres","dsn":"`+secret)

	_, err := (environmentSourceResolver{}).ResolveSource(context.Background(), "bad")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}

func TestEnvironmentSourceResolverRejectsArbitraryEnvironmentNames(t *testing.T) {
	require.NoError(t, os.Setenv("UNRELATED_SECRET", "secret"))
	t.Cleanup(func() { _ = os.Unsetenv("UNRELATED_SECRET") })

	_, err := (environmentSourceResolver{}).ResolveSource(context.Background(), "../UNRELATED_SECRET")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "letters, numbers, and underscores")
}

func TestBinaryEndToEndOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go run end-to-end test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "run", ".", "-driver", "sqlite", "-source", "primary", "-allow-query")
	command.Env = append(os.Environ(),
		initialDSNEnv+"=:memory:",
		"GOCACHE=/tmp/dbstore-go-cache",
	)
	client := mcp.NewClient(&mcp.Implementation{Name: "dbstore-mcp-e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()

	sourcesResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "db_list_sources"})
	require.NoError(t, err)
	require.False(t, sourcesResult.IsError)
	var sources mcpserver.ListSourcesOutput
	decodeStructuredContent(t, sourcesResult, &sources)
	require.Len(t, sources.Sources, 1)
	assert.Equal(t, "primary", sources.Sources[0].Name)
	assert.Equal(t, "sqlite", sources.Sources[0].Driver)

	pingResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "db_ping",
		Arguments: map[string]any{"source": "primary"},
	})
	require.NoError(t, err)
	require.False(t, pingResult.IsError)

	queryResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "db_query",
		Arguments: map[string]any{
			"source": "primary",
			"query":  "SELECT 42 AS answer",
		},
	})
	require.NoError(t, err)
	require.False(t, queryResult.IsError)
	var query mcpserver.QueryOutput
	decodeStructuredContent(t, queryResult, &query)
	assert.Equal(t, []string{"answer"}, query.Columns)
	require.Len(t, query.Rows, 1)
	assert.Equal(t, float64(42), query.Rows[0][0])
}

func decodeStructuredContent(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}
