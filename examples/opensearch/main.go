package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/loykin/dbstore"
	restadapter "github.com/loykin/dbstore/adapters/rest"
)

type Document struct {
	ID   string `json:"-"`
	Name string `json:"name"`
}

type DocumentRepository interface {
	Create(ctx context.Context, id, name string) error
	FindByID(ctx context.Context, id string) (*Document, error)
}

type openSearchDocumentRepo struct {
	restadapter.Source
	index string
}

var _ DocumentRepository = (*openSearchDocumentRepo)(nil)

func NewDocumentRepo(exec *dbstore.Executor[*restadapter.Client], source, index string) DocumentRepository {
	return &openSearchDocumentRepo{
		Source: restadapter.NewSource(source, exec),
		index:  index,
	}
}

func (r *openSearchDocumentRepo) Create(ctx context.Context, id, name string) error {
	return r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		return client.DoJSON(ctx, http.MethodPut, "/"+r.index+"/_create/"+id, Document{Name: name}, nil)
	})
}

func (r *openSearchDocumentRepo) FindByID(ctx context.Context, id string) (*Document, error) {
	var doc Document
	err := r.Run(ctx, func(ctx context.Context, client *restadapter.Client) error {
		var resp struct {
			Found  bool            `json:"found"`
			Source json.RawMessage `json:"_source"`
		}
		if err := client.DoJSON(ctx, http.MethodGet, "/"+r.index+"/_doc/"+id, nil, &resp); err != nil {
			return err
		}
		if !resp.Found {
			return fmt.Errorf("document %q not found", id)
		}
		return json.Unmarshal(resp.Source, &doc)
	})
	doc.ID = id
	return &doc, err
}

func setupStore(address string) (DocumentRepository, func(), error) {
	registry := dbstore.NewDriverRegistry[*restadapter.Client]()
	registry.Register("rest", restadapter.Driver{})

	pool := dbstore.NewPool(registry)
	cleanup := pool.RemoveAll

	if err := pool.Register("search", dbstore.DriverConfig{
		Driver: "rest",
		DSN:    address,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	exec := dbstore.NewExecutor(pool)
	return NewDocumentRepo(exec, "search", "users"), cleanup, nil
}

func main() {
	ctx := context.Background()
	server := newFakeOpenSearchServer()
	defer server.Close()

	repo, cleanup, err := setupStore(server.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	if err := repo.Create(ctx, "1", "Alice"); err != nil {
		log.Fatal(err)
	}

	doc, err := repo.FindByID(ctx, "1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %s\n", doc.ID, doc.Name)
}

func newFakeOpenSearchServer() *httptest.Server {
	var (
		mu   sync.Mutex
		docs = make(map[string]json.RawMessage)
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}

		index, action, id := parts[0], parts[1], parts[2]
		key := index + "/" + id

		switch {
		case r.Method == http.MethodPut && action == "_create":
			var body json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			docs[key] = body
			mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]string{"_id": id, "result": "created"})

		case r.Method == http.MethodGet && action == "_doc":
			mu.Lock()
			body, ok := docs[key]
			mu.Unlock()
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"found": false, "_id": id})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"found": true, "_id": id, "_source": body})

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
