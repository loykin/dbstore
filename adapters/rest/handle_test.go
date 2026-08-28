package restadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/loykin/dbstore"
)

func newTestHandleClient(t *testing.T, handler http.HandlerFunc) Handle {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	return Handle{c: &Client{HTTPClient: server.Client(), BaseURL: baseURL}}
}

func TestHandle_Get_NotFoundTranslatesToErrNotFound(t *testing.T) {
	a := newTestHandleClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	var dest struct{ Name string }
	err := a.Get(context.Background(), "/users/999", &dest)
	if !errors.Is(err, dbstore.ErrNotFound) {
		t.Fatalf("want dbstore.ErrNotFound, got %v", err)
	}
}

func TestHandle_Get_OtherStatusPassesThrough(t *testing.T) {
	a := newTestHandleClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	var dest struct{ Name string }
	err := a.Get(context.Background(), "/users/1", &dest)
	if errors.Is(err, dbstore.ErrNotFound) {
		t.Fatal("500 should not be treated as not-found")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want *StatusError with 500, got %v", err)
	}
}

func TestHandle_Post(t *testing.T) {
	var gotBody string
	a := newTestHandleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		gotBody = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
	})
	if err := a.Post(context.Background(), "/users", map[string]string{"name": "Alice"}); err != nil {
		t.Fatal(err)
	}
	if gotBody != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotBody)
	}
}
