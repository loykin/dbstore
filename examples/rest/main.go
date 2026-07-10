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

// bearerTokenTransport shows the extension point for auth that must be
// computed per request or refreshed over time (OAuth2 token refresh,
// request signing, mTLS, ...): wrap Driver.HTTPClient's Transport in a
// custom http.RoundTripper. Real code would fetch/refresh a token from an
// auth provider instead of counting requests.
type bearerTokenTransport struct {
	base   http.RoundTripper
	issued int
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.issued++
	token := fmt.Sprintf("token-%d", t.issued)

	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

type Secret struct {
	Value string `json:"value"`
}

type SecretRepo struct {
	source restadapter.Source
}

func NewSecretRepo(exec *dbstore.Executor[*restadapter.Client], source string) *SecretRepo {
	return &SecretRepo{source: restadapter.NewSource(source, exec)}
}

func (r *SecretRepo) Find(ctx context.Context) (*Secret, error) {
	var secret Secret
	err := r.source.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodGet, "/secret", nil, &secret)
	})
	return &secret, err
}

func setupStore(userServerURL, secretServerURL string) (*UserRepo, *SecretRepo, func(), error) {
	rest := restadapter.New()

	// Static credentials: a fixed value goes straight in Header. BasicAuth
	// covers the common Basic Auth case; an API key would be
	// http.Header{"X-Api-Key": []string{key}} the same way.
	rest.RegisterDriver("basic-auth", restadapter.Driver{
		Header: restadapter.BasicAuth("app", "s3cret"),
	})

	// Dynamic credentials: HTTPClient carries a custom RoundTripper instead.
	rest.RegisterDriver("bearer-auth", restadapter.Driver{
		HTTPClient: &http.Client{Transport: &bearerTokenTransport{}},
	})

	cleanup := rest.Close

	if err := rest.Open("users-api", dbstore.SourceConfig{
		Driver: "basic-auth",
		DSN:    userServerURL,
	}); err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	if err := rest.Open("secret-api", dbstore.SourceConfig{
		Driver: "bearer-auth",
		DSN:    secretServerURL,
	}); err != nil {
		cleanup()
		return nil, nil, nil, err
	}

	exec := rest.Executor()
	return NewUserRepo(exec, "users-api"), NewSecretRepo(exec, "secret-api"), cleanup, nil
}

func main() {
	ctx := context.Background()
	userServer := newUserServer()
	defer userServer.Close()
	secretServer := newSecretServer()
	defer secretServer.Close()

	users, secrets, cleanup, err := setupStore(userServer.URL, secretServer.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	user, err := users.Find(ctx, "1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %s\n", user.ID, user.Name)

	secret, err := secrets.Find(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("secret:", secret.Value)
}

func newUserServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "app" || password != "s3cret" {
			http.Error(w, "invalid basic auth", http.StatusUnauthorized)
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

func newSecretServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/secret" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Secret{Value: "classified"})
	}))
}
