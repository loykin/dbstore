package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/dbstore/mcpserver"
)

const (
	initialDSNEnv      = "DBSTORE_MCP_DSN"
	sourceConfigPrefix = "DBSTORE_MCP_SOURCE_"
)

var configRefPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type environmentSourceResolver struct{}

func (environmentSourceResolver) ResolveSource(_ context.Context, ref string) (dbstore.SourceConfig, error) {
	if !configRefPattern.MatchString(ref) {
		return dbstore.SourceConfig{}, fmt.Errorf("configuration reference may contain only letters, numbers, and underscores")
	}
	envName := sourceConfigPrefix + strings.ToUpper(ref)
	value, ok := os.LookupEnv(envName)
	if !ok {
		return dbstore.SourceConfig{}, fmt.Errorf("source configuration reference %q was not found", ref)
	}
	var cfg dbstore.SourceConfig
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return dbstore.SourceConfig{}, fmt.Errorf("source configuration reference %q is invalid: %w", ref, err)
	}
	if cfg.Driver == "" {
		return dbstore.SourceConfig{}, fmt.Errorf("source configuration reference %q has no driver", ref)
	}
	return cfg, nil
}

func main() {
	var (
		source         = flag.String("source", "primary", "name for the optional initial source")
		driver         = flag.String("driver", sqlxadapter.DriverSQLite, "driver for the optional initial source")
		maxConcurrency = flag.Int("max-concurrency", dbstore.DefaultPoolConfig.MaxConcurrency, "per-source dbstore concurrency limit")
		allowQuery     = flag.Bool("allow-query", false, "allow bounded SELECT queries; use a database account with narrow grants")
		allowManage    = flag.Bool("allow-manage", false, "allow source registration/removal using server-side environment references")
	)
	flag.Parse()

	store := sqlxadapter.New()
	store.RegisterDefaultDrivers()
	defer store.Close()

	if dsn := os.Getenv(initialDSNEnv); dsn != "" {
		cfg := dbstore.SourceConfig{
			Driver: *driver,
			DSN:    dsn,
			PoolConfig: dbstore.PoolConfig{
				MaxOpenConns:   dbstore.DefaultPoolConfig.MaxOpenConns,
				MaxIdleConns:   dbstore.DefaultPoolConfig.MaxIdleConns,
				MaxLifetime:    dbstore.DefaultPoolConfig.MaxLifetime,
				MaxIdleTime:    dbstore.DefaultPoolConfig.MaxIdleTime,
				MaxConcurrency: *maxConcurrency,
			},
		}
		if err := store.Open(*source, cfg); err != nil {
			log.Fatalf("open initial source %q failed", *source)
		}
	}

	options := mcpserver.Options{
		Store: store,
		Policy: mcpserver.CapabilityPolicy{
			AllowQuery:  *allowQuery,
			AllowManage: *allowManage,
		},
	}
	if *allowManage {
		options.EnableManagement = true
		options.SourceResolver = environmentSourceResolver{}
		log.Printf("source management enabled; configuration references resolve from %s<REF>", sourceConfigPrefix)
	}
	server, err := mcpserver.New(options)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.ServeStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
