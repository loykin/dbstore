package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/loykin/dbstore"
	prometheusadapter "github.com/loykin/dbstore/adapters/prometheus"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	registry := prometheus.NewRegistry()
	observer := prometheusadapter.New("example_sql", registry)

	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	sql.SetObserver(observer) // one call — every Run below is now instrumented
	defer sql.Close()

	poolCfg := dbstore.PoolConfig{MaxOpenConns: 1, MaxIdleConns: 1, MaxConcurrency: 1}
	if err := sql.Open("primary", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite, DSN: ":memory:", PoolConfig: poolCfg,
	}); err != nil {
		log.Fatal(err)
	}
	// A second source, opened after "primary", so sources_active and the
	// per-source event/wait/run series below aren't just single-sample.
	if err := sql.Open("secondary", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite, DSN: ":memory:", PoolConfig: poolCfg,
	}); err != nil {
		log.Fatal(err)
	}

	exec := sql.Executor()

	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	// A few successful operations, and one failing one, so the metrics below
	// show both a status="ok" and a status="error" series.
	for _, name := range []string{"Alice", "Bob"} {
		if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
			return err
		}); err != nil {
			log.Fatal(err)
		}
	}
	_ = exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO no_such_table (name) VALUES ('x')`)
		return err
	})

	// A single call on "secondary" so its wait/run series show up too, and
	// source_events_total/sources_active reflect both sources having been
	// registered — not just "primary".
	if err := exec.Run(ctx, "secondary", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	// Expose /metrics the same way a real service would, then scrape it.
	server := httptest.NewServer(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "example_sql_") {
			fmt.Println(line)
		}
	}
}
