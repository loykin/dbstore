package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/loykin/dbstore"
	restadapter "github.com/loykin/dbstore/adapters/rest"
)

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type UserRepo struct {
	source restadapter.Source
}

func NewUserRepo(exec *dbstore.Executor[*restadapter.Client], source string) *UserRepo {
	return &UserRepo{source: restadapter.NewSource(source, exec)}
}

func (r *UserRepo) Find(ctx context.Context, id string) (*User, error) {
	var user User
	err := r.source.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodGet, "/users/"+id, nil, &user)
	})
	return &user, err
}

func setupStore(baseURL string) (*UserRepo, func(), error) {
	rest := restadapter.New()
	rest.RegisterDriver("json-api", restadapter.Driver{
		Header: http.Header{"X-App": []string{"dbstore-example"}},
	})
	cleanup := rest.Close

	if err := rest.Open("users-api", dbstore.SourceConfig{
		Driver: "json-api",
		DSN:    baseURL,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	return NewUserRepo(rest.Executor(), "users-api"), cleanup, nil
}

func main() {
	ctx := context.Background()
	server := newUserServer()
	defer server.Close()

	repo, cleanup, err := setupStore(server.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	user, err := repo.Find(ctx, "1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %s\n", user.ID, user.Name)
}

func newUserServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-App") != "dbstore-example" {
			http.Error(w, "missing X-App header", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/users/1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(User{ID: "1", Name: "Alice"})
	}))
}
