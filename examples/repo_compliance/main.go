package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	restadapter "github.com/loykin/dbstore/adapters/rest"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"
)

// --- SQLite wiring ---

func setupSQLite(ctx context.Context) (UserRepository, func(), error) {
	sql := sqlxadapter.New()
	sql.RegisterDefaultDrivers()
	cleanup := sql.Close

	if err := sql.Open("primary", dbstore.SourceConfig{
		Driver: sqlxadapter.DriverSQLite,
		DSN:    ":memory:",
		PoolConfig: dbstore.PoolConfig{
			MaxOpenConns:   1,
			MaxIdleConns:   1,
			MaxConcurrency: 1,
		},
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	exec := sql.Executor()
	if err := exec.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		// UNIQUE on name lets main_test.go's CreateBatch_Rollback trigger a
		// genuine mid-batch failure (a duplicate name) using only the
		// UserRepository interface, instead of reaching for a
		// backend-specific type to force one.
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)`)
		return err
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	repo := NewUserRepo[sqlxadapter.Adaptor](SqliteUserTemplate{}, sql.Source("primary"))
	return repo, cleanup, nil
}

// --- REST wiring ---

func setupREST(baseURL string) (UserRepository, func(), error) {
	rest := restadapter.New()
	rest.RegisterDriver("json-api", restadapter.Driver{})
	cleanup := rest.Close

	if err := rest.Open("primary", dbstore.SourceConfig{
		Driver: "json-api",
		DSN:    baseURL,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	repo := NewUserRepo[restadapter.Adaptor](RestUserTemplate{}, rest.Source("primary"))
	return repo, cleanup, nil
}

// newFakeUsersServer is a minimal in-memory JSON Users API standing in for a
// real service — enough to prove the REST-backed UserRepository behaves like
// the SQLite one, not a realistic REST API design.
func newFakeUsersServer() *httptest.Server {
	var (
		mu     sync.Mutex
		users  []User
		nextID = 1
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/users":
			var body User
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			body.ID = nextID
			nextID++
			users = append(users, body)
			mu.Unlock()
			writeJSON(w, http.StatusCreated, body)

		case r.Method == http.MethodGet && r.URL.Path == "/users":
			mu.Lock()
			defer mu.Unlock()
			writeJSON(w, http.StatusOK, users)

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/"):
			id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/users/"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, u := range users {
				if u.ID == id {
					writeJSON(w, http.StatusOK, u)
					return
				}
			}
			http.NotFound(w, r)

		default:
			http.NotFound(w, r)
		}
	}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func runDemo(ctx context.Context, label string, repo UserRepository) {
	if err := repo.Create(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}
	user, err := repo.FindByID(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[%s] %d: %s\n", label, user.ID, user.Name)
}

func main() {
	ctx := context.Background()

	sqliteRepo, sqliteCleanup, err := setupSQLite(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer sqliteCleanup()
	runDemo(ctx, "sqlite", sqliteRepo)

	server := newFakeUsersServer()
	defer server.Close()
	restRepo, restCleanup, err := setupREST(server.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer restCleanup()
	runDemo(ctx, "rest", restRepo)
}
