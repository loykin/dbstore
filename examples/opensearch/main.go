package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/loykin/dbstore"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type OpenSearchDriver struct{}

func (d *OpenSearchDriver) Open(cfg dbstore.DriverConfig) (*opensearchapi.Client, error) {
	return opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{cfg.DSN}},
	})
}

type Document struct {
	ID   string `json:"-"`
	Name string `json:"name"`
}

type DocumentRepository interface {
	Create(ctx context.Context, id, name string) error
	FindByID(ctx context.Context, id string) (*Document, error)
}

type openSearchDocumentRepo struct {
	dbstore.BaseRepo[*opensearchapi.Client]
	index string
}

var _ DocumentRepository = (*openSearchDocumentRepo)(nil)

func NewDocumentRepo(exec *dbstore.Executor[*opensearchapi.Client], source, index string) DocumentRepository {
	return &openSearchDocumentRepo{
		BaseRepo: dbstore.NewBaseRepo(source, exec),
		index:    index,
	}
}

func (r *openSearchDocumentRepo) Create(ctx context.Context, id, name string) error {
	return r.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
		body, err := json.Marshal(Document{Name: name})
		if err != nil {
			return err
		}
		_, err = client.Document.Create(ctx, opensearchapi.DocumentCreateReq{
			Index:      r.index,
			DocumentID: id,
			Body:       bytes.NewReader(body),
		})
		return err
	})
}

func (r *openSearchDocumentRepo) FindByID(ctx context.Context, id string) (*Document, error) {
	var doc Document
	err := r.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
		resp, err := client.Document.Get(ctx, opensearchapi.DocumentGetReq{
			Index:      r.index,
			DocumentID: id,
		})
		if err != nil {
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
	registry := dbstore.NewDriverRegistry[*opensearchapi.Client]()
	registry.Register("opensearch", &OpenSearchDriver{})

	pool := dbstore.NewPool(registry)
	cleanup := pool.RemoveAll

	if err := pool.Register("search", dbstore.DriverConfig{
		Driver: "opensearch",
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
