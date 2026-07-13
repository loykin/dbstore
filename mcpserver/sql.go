package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type TableInfo struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey,omitempty"`
}

func (s *Server) listTables(ctx context.Context, _ *mcp.CallToolRequest, in SourceInput) (*mcp.CallToolResult, ListTablesOutput, error) {
	if err := requireSource(in.Source); err != nil {
		return nil, ListTablesOutput{}, err
	}
	if err := s.policy.Authorize(ctx, OperationListTables, in.Source); err != nil {
		return nil, ListTablesOutput{}, err
	}

	output := ListTablesOutput{Source: in.Source, Tables: []TableInfo{}}
	err := s.store.Executor().Run(ctx, in.Source, func(ctx context.Context, db *sqlx.DB) error {
		query, err := listTablesQuery(db.DriverName())
		if err != nil {
			return err
		}
		rows, err := db.QueryxContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var table TableInfo
			if err := rows.Scan(&table.Schema, &table.Name, &table.Type); err != nil {
				return err
			}
			output.Tables = append(output.Tables, table)
		}
		return rows.Err()
	})
	return nil, output, err
}

func listTablesQuery(driver string) (string, error) {
	switch driver {
	case "sqlite", "sqlite3":
		return `SELECT '' AS schema_name, name, type
			FROM sqlite_master
			WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
			ORDER BY name`, nil
	case "postgres", "pgx":
		return `SELECT table_schema, table_name, table_type
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			ORDER BY table_name`, nil
	case "mysql":
		return `SELECT table_schema, table_name, table_type
			FROM information_schema.tables
			WHERE table_schema = DATABASE()
			ORDER BY table_name`, nil
	default:
		return "", fmt.Errorf("mcpserver: schema inspection is unsupported for SQL driver %q", driver)
	}
}

func (s *Server) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in DescribeTableInput) (*mcp.CallToolResult, DescribeTableOutput, error) {
	if err := requireSource(in.Source); err != nil {
		return nil, DescribeTableOutput{}, err
	}
	if in.Table == "" {
		return nil, DescribeTableOutput{}, fmt.Errorf("mcpserver: table is required")
	}
	if err := s.policy.Authorize(ctx, OperationDescribeTable, in.Source); err != nil {
		return nil, DescribeTableOutput{}, err
	}

	output := DescribeTableOutput{Source: in.Source, Table: in.Table, Columns: []ColumnInfo{}}
	err := s.store.Executor().Run(ctx, in.Source, func(ctx context.Context, db *sqlx.DB) error {
		var err error
		switch db.DriverName() {
		case "sqlite", "sqlite3":
			err = describeSQLite(ctx, db, in.Table, &output.Columns)
		case "postgres", "pgx":
			err = describeInformationSchema(ctx, db, in.Table, true, &output.Columns)
		case "mysql":
			err = describeInformationSchema(ctx, db, in.Table, false, &output.Columns)
		default:
			err = fmt.Errorf("mcpserver: schema inspection is unsupported for SQL driver %q", db.DriverName())
		}
		return err
	})
	if err == nil && len(output.Columns) == 0 {
		err = fmt.Errorf("mcpserver: table %q was not found", in.Table)
	}
	return nil, output, err
}

func describeSQLite(ctx context.Context, db *sqlx.DB, table string, out *[]ColumnInfo) error {
	rows, err := db.QueryxContext(ctx,
		`SELECT name, type, "notnull", pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var column ColumnInfo
		var notNull, primaryKey int
		if err := rows.Scan(&column.Name, &column.Type, &notNull, &primaryKey); err != nil {
			return err
		}
		column.Nullable = notNull == 0
		column.PrimaryKey = primaryKey > 0
		*out = append(*out, column)
	}
	return rows.Err()
}

func describeInformationSchema(ctx context.Context, db *sqlx.DB, table string, postgres bool, out *[]ColumnInfo) error {
	schemaPredicate := "table_schema = DATABASE()"
	placeholder := "?"
	if postgres {
		schemaPredicate = "table_schema = current_schema()"
		placeholder = "$1"
	}
	query := `SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE ` + schemaPredicate + ` AND table_name = ` + placeholder + `
		ORDER BY ordinal_position`
	rows, err := db.QueryxContext(ctx, query, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var column ColumnInfo
		var nullable string
		if err := rows.Scan(&column.Name, &column.Type, &nullable); err != nil {
			return err
		}
		column.Nullable = nullable == "YES"
		*out = append(*out, column)
	}
	return rows.Err()
}

var selectIntoPattern = regexp.MustCompile(`(?i)\bINTO\b`)

func validateReadOnlyQuery(query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("mcpserver: query is required")
	}
	if strings.ContainsRune(query, 0) {
		return "", fmt.Errorf("mcpserver: query contains a NUL byte")
	}
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if strings.Contains(query, ";") {
		return "", fmt.Errorf("mcpserver: exactly one SQL statement is allowed")
	}
	fields := strings.Fields(query)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "SELECT") {
		return "", fmt.Errorf("mcpserver: only SELECT statements are allowed")
	}
	if selectIntoPattern.MatchString(query) {
		return "", fmt.Errorf("mcpserver: SELECT INTO is not allowed")
	}
	return query, nil
}

func (s *Server) query(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, QueryOutput, error) {
	if err := requireSource(in.Source); err != nil {
		return nil, QueryOutput{}, err
	}
	if err := s.policy.Authorize(ctx, OperationQuery, in.Source); err != nil {
		return nil, QueryOutput{}, err
	}
	query, err := validateReadOnlyQuery(in.Query)
	if err != nil {
		return nil, QueryOutput{}, err
	}
	maxRows := in.MaxRows
	if maxRows <= 0 {
		maxRows = s.defaultMaxRows
	}
	if maxRows > s.maximumMaxRows {
		return nil, QueryOutput{}, fmt.Errorf("mcpserver: maxRows cannot exceed %d", s.maximumMaxRows)
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = s.defaultMaxBytes
	}
	if maxBytes > s.maximumMaxBytes {
		return nil, QueryOutput{}, fmt.Errorf("mcpserver: maxBytes cannot exceed %d", s.maximumMaxBytes)
	}
	timeout := 5 * time.Second
	if in.TimeoutMS != 0 {
		if in.TimeoutMS < 1 || in.TimeoutMS > 30000 {
			return nil, QueryOutput{}, fmt.Errorf("mcpserver: timeoutMs must be between 1 and 30000")
		}
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output := QueryOutput{Source: in.Source, Rows: [][]any{}}
	err = s.store.Executor().Run(queryCtx, in.Source, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()

		rows, err := tx.QueryxContext(ctx, query, in.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		output.Columns, err = rows.Columns()
		if err != nil {
			return err
		}
		encodedColumns, err := json.Marshal(output.Columns)
		if err != nil {
			return err
		}
		responseBytes := len(in.Source) + len(encodedColumns) + 128
		if responseBytes > maxBytes {
			return fmt.Errorf("mcpserver: query result exceeds maxBytes limit of %d", maxBytes)
		}
		for rows.Next() {
			values := make([]any, len(output.Columns))
			destinations := make([]any, len(values))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := rows.Scan(destinations...); err != nil {
				return err
			}
			if len(output.Rows) == maxRows {
				output.Truncated = true
				break
			}
			row := make([]any, len(output.Columns))
			for i := range output.Columns {
				row[i] = normalizeSQLValue(values[i])
			}
			encodedRow, err := json.Marshal(row)
			if err != nil {
				return fmt.Errorf("mcpserver: encode query row: %w", err)
			}
			if responseBytes+len(encodedRow)+1 > maxBytes {
				return fmt.Errorf("mcpserver: query result exceeds maxBytes limit of %d", maxBytes)
			}
			responseBytes += len(encodedRow) + 1
			output.Rows = append(output.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		output.RowCount = len(output.Rows)
		encodedOutput, err := json.Marshal(output)
		if err != nil {
			return fmt.Errorf("mcpserver: encode query result: %w", err)
		}
		if len(encodedOutput) > maxBytes {
			return fmt.Errorf("mcpserver: query result exceeds maxBytes limit of %d", maxBytes)
		}
		return nil
	})
	return nil, output, err
}

func normalizeSQLValue(value any) any {
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return value
}
