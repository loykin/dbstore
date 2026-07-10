package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	restadapter "github.com/loykin/dbstore/adapters/rest"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed config.json
var configJSON []byte

// AppConfig is owned by the application, not dbstore — dbstore.Config only
// describes the sources for one Adapter[T], so a config file spanning
// multiple backend types (here: sql and rest) nests one dbstore.Config per
// backend under whatever top-level shape the application wants.
type AppConfig struct {
	SQL  dbstore.Config `json:"sql"`
	REST dbstore.Config `json:"rest"`
}

type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (string, error)
}

type userRepo struct {
	sqlxadapter.Source
}

func NewUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) UserRepository {
	return &userRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *userRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *userRepo) FindByID(ctx context.Context, id int) (string, error) {
	var name string
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, id).Scan(&name)
	})
	return name, err
}

type StatsRepository interface {
	RecordLogin(ctx context.Context, count int) error
	LoginCount(ctx context.Context) (int, error)
}

type statsRepo struct {
	sqlxadapter.Source
}

func NewStatsRepo(exec *dbstore.Executor[*sqlx.DB], source string) StatsRepository {
	return &statsRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *statsRepo) RecordLogin(ctx context.Context, count int) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO events (type, count) VALUES (?, ?)`, "login", count)
		return err
	})
}

func (r *statsRepo) LoginCount(ctx context.Context) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.QueryRowContext(ctx, `SELECT count FROM events WHERE type = 'login'`).Scan(&count)
	})
	return count, err
}

// DirectoryRepository looks up a display name from an external REST
// directory service — a source config.json also configures, alongside the
// two SQL sources, to show Config isn't SQL-specific.
type DirectoryRepository interface {
	FindName(ctx context.Context, id string) (string, error)
}

type directoryRepo struct {
	restadapter.Source
}

func NewDirectoryRepo(exec *dbstore.Executor[*restadapter.Client], source string) DirectoryRepository {
	return &directoryRepo{Source: restadapter.NewSource(source, exec)}
}

func (r *directoryRepo) FindName(ctx context.Context, id string) (string, error) {
	var resp struct {
		Name string `json:"name"`
	}
	err := r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodGet, "/directory/"+id, nil, &resp)
	})
	return resp.Name, err
}

func main() {
	ctx := context.Background()

	// dbstore does not load JSON/YAML itself — the application decodes it
	// into dbstore.Config (one per backend) and hands each to its adapter.
	var appCfg AppConfig
	if err := json.Unmarshal(configJSON, &appCfg); err != nil {
		log.Fatal(err)
	}

	// The directory service address isn't known until runtime here (it's a
	// test server); a real deployment would get it the same way any other
	// DSN comes from outside the checked-in file, e.g. an env var overlay
	// applied after unmarshalling and before Configure.
	directoryServer := newFakeDirectoryServer()
	defer directoryServer.Close()
	directorySource := appCfg.REST.Sources["directory"]
	directorySource.DSN = directoryServer.URL
	appCfg.REST.Sources["directory"] = directorySource

	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	defer sql.Close()

	rest := restadapter.New()
	rest.RegisterDriver("json-api", restadapter.Driver{})
	defer rest.Close()

	// Configure opens every source in its Sources map in one call. If any
	// source fails to open, sources already opened by that call are closed
	// again before the error is returned — Configure is all-or-nothing.
	if err := sql.Configure(appCfg.SQL); err != nil {
		log.Fatal(err)
	}
	if err := rest.Configure(appCfg.REST); err != nil {
		log.Fatal(err)
	}

	exec := sql.Executor()

	// "meta" and "stats" are names the application already knows at compile
	// time (see config.json) — config only externalizes *how* to open each
	// source (driver/DSN/pool), not *what they're called*. Repositories
	// embed sqlxadapter.Source the same way they would if the source had
	// been opened with a single Open call instead of Configure.
	if err := exec.Run(ctx, "meta", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}
	if err := exec.Run(ctx, "stats", func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, type TEXT, count INTEGER)`)
		return err
	}); err != nil {
		log.Fatal(err)
	}

	users := NewUserRepo(exec, "meta")
	stats := NewStatsRepo(exec, "stats")
	directory := NewDirectoryRepo(rest.Executor(), "directory")

	if err := users.Create(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}
	if err := stats.RecordLogin(ctx, 42); err != nil {
		log.Fatal(err)
	}

	name, err := users.FindByID(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[meta] user:", name)

	count, err := stats.LoginCount(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[stats] login count:", count)

	displayName, err := directory.FindName(ctx, "1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[directory] display name:", displayName)
}

func newFakeDirectoryServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/directory/1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "Alice Anderson"})
	}))
}
