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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
	opensearchadapter "github.com/loykin/dbstore/adapters/opensearch"
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
	source opensearchadapter.Source
	index  string
}

var _ DocumentRepository = (*openSearchDocumentRepo)(nil)

func NewDocumentRepo(exec *dbstore.Executor[*opensearchapi.Client], source, index string) DocumentRepository {
	return &openSearchDocumentRepo{
		source: opensearchadapter.NewSource(source, exec),
		index:  index,
	}
}

func (r *openSearchDocumentRepo) Create(ctx context.Context, id, name string) error {
	return r.source.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
		body, err := json.Marshal(Document{Name: name})
		if err != nil {
			return err
		}
		_, err = client.Index(ctx, opensearchapi.IndexReq{
			Index:      r.index,
			DocumentID: id,
			Body:       bytes.NewReader(body),
		})
		return err
	})
}

func (r *openSearchDocumentRepo) FindByID(ctx context.Context, id string) (*Document, error) {
	var doc Document
	err := r.source.Run(ctx, func(ctx context.Context, client *opensearchapi.Client) error {
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
	search := opensearchadapter.New()
	search.RegisterDriver("opensearch", opensearchadapter.Driver{})
	cleanup := search.Close

	if err := search.Open("search", dbstore.SourceConfig{
		Driver: "opensearch",
		DSN:    address,
	}); err != nil {
		cleanup()
		return nil, nil, err
	}

	return NewDocumentRepo(search.Executor(), "search", "users"), cleanup, nil
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
				"_shards": map[string]int{
					"total":      1,
					"successful": 1,
					"failed":     0,
				},
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
