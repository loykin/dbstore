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

type User struct {
	ID   int    `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

// UserRepository is the one contract both backends below implement. The
// compliance suite in main_test.go only ever calls these four methods, so
// it runs unchanged against either implementation — the concrete proof for
// the root README's "Why" claim.
type UserRepository interface {
	Create(ctx context.Context, name string) error
	FindByID(ctx context.Context, id int) (*User, error)
	FindAll(ctx context.Context) ([]User, error)
	CreateBatch(ctx context.Context, names []string) error
}

// --- SQLite implementation ---

type sqliteUserRepo struct {
	sqlxadapter.Source
}

func newSQLiteUserRepo(exec *dbstore.Executor[*sqlx.DB], source string) UserRepository {
	return &sqliteUserRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *sqliteUserRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES (?)`, name)
		return err
	})
}

func (r *sqliteUserRepo) FindByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &u, `SELECT id, name FROM users WHERE id = ?`, id)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *sqliteUserRepo) FindAll(ctx context.Context) ([]User, error) {
	var users []User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &users, `SELECT id, name FROM users ORDER BY id`)
	})
	return users, err
}

func (r *sqliteUserRepo) CreateBatch(ctx context.Context, names []string) error {
	for _, name := range names {
		if err := r.Create(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

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
		_, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
		return err
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	return newSQLiteUserRepo(exec, "primary"), cleanup, nil
}

// --- REST implementation ---

type restUserRepo struct {
	restadapter.Source
}

func newRESTUserRepo(exec *dbstore.Executor[*restadapter.Client], source string) UserRepository {
	return &restUserRepo{Source: restadapter.NewSource(source, exec)}
}

func (r *restUserRepo) Create(ctx context.Context, name string) error {
	return r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodPost, "/users", User{Name: name}, nil)
	})
}

func (r *restUserRepo) FindByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodGet, fmt.Sprintf("/users/%d", id), nil, &u)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *restUserRepo) FindAll(ctx context.Context) ([]User, error) {
	var users []User
	err := r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodGet, "/users", nil, &users)
	})
	return users, err
}

func (r *restUserRepo) CreateBatch(ctx context.Context, names []string) error {
	for _, name := range names {
		if err := r.Create(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

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

	return newRESTUserRepo(rest.Executor(), "primary"), cleanup, nil
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
