package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/loykin/dbstore"
	elasticsearchadapter "github.com/loykin/dbstore/adapters/elasticsearch"
)

type Document struct {
	Name string `json:"name"`
}

type DocumentRepo struct {
	source elasticsearchadapter.Source
	index  string
}

func NewDocumentRepo(source elasticsearchadapter.Source, index string) *DocumentRepo {
	return &DocumentRepo{source: source, index: index}
}

func (r *DocumentRepo) Save(ctx context.Context, id string, doc Document) error {
	return r.source.Run(ctx, func(ctx context.Context, a elasticsearchadapter.Adaptor) error {
		return a.Index(ctx, r.index, id, doc)
	})
}

func (r *DocumentRepo) Find(ctx context.Context, id string) (*Document, error) {
	var doc Document
	err := r.source.Run(ctx, func(ctx context.Context, a elasticsearchadapter.Adaptor) error {
		return a.Get(ctx, r.index, id, &doc)
	})
	if errors.Is(err, dbstore.ErrNotFound) {
		return nil, fmt.Errorf("document %q not found", id)
	}
	return &doc, err
}

func setupStore(address string) (*DocumentRepo, func(), error) {
	search := elasticsearchadapter.New()
	search.RegisterDriver("elasticsearch", elasticsearchadapter.Driver{})
	cleanup := search.Close

	if err := search.Open("primary", dbstore.SourceConfig{
		Driver: "elasticsearch",
		DSN:    address,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	return NewDocumentRepo(search.Source("primary"), "users"), cleanup, nil
}

func main() {
	ctx := context.Background()
	server := newFakeElasticsearchServer()
	defer server.Close()

	repo, cleanup, err := setupStore(server.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	if err := repo.Save(ctx, "1", Document{Name: "Alice"}); err != nil {
		log.Fatal(err)
	}

	doc, err := repo.Find(ctx, "1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Name)
}

func newFakeElasticsearchServer() *httptest.Server {
	var (
		mu   sync.Mutex
		docs = make(map[string]json.RawMessage)
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}

		index, action, id := parts[0], parts[1], parts[2]
		if action != "_doc" {
			http.NotFound(w, r)
			return
		}
		key := index + "/" + id

		switch r.Method {
		case http.MethodPut:
			var body json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			docs[key] = body
			mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{
				"_index": index,
				"_id":    id,
				"result": "created",
			})

		case http.MethodGet:
			mu.Lock()
			body, ok := docs[key]
			mu.Unlock()
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]any{
					"_index": index,
					"_id":    id,
					"found":  false,
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"_index":  index,
				"_id":     id,
				"_source": body,
				"found":   true,
			})

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
