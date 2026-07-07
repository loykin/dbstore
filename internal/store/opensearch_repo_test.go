//go:build integration

// This file finishes what opensearch_research_test.go started: it
// instantiates the actual generic Pool[T]/Executor[T]/BaseRepo[T] from
// pool.go/executor.go/repo.go with T = *opensearchapi.Client, end to end,
// against a real OpenSearch container.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"
)

// Doc is the document-store analogue of User (repo_test.go): a domain
// model backed by an OpenSearch document instead of a SQL row.
type Doc struct {
	ID   string `json:"-"`
	Name string `json:"name"`
}

// DocRepository is the document-store analogue of UserRepository. It has no
// CreateBatch/transactional method by design: OpenSearch's bulk API reports
// success/failure per item rather than committing atomically, so it isn't
// a drop-in replacement for SQL's RunTx — see requirements.md's operation
// shape argument.
type DocRepository interface {
	Create(ctx context.Context, id, name string) error
	FindByID(ctx context.Context, id string) (*Doc, error)
}

// openSearchDriver implements DriverBuilder[*opensearchapi.Client]. It has
// no ApplyPoolConfig: PoolConfigApplier is an optional capability precisely
// so drivers like this one — where none of PoolConfig's fields (MaxOpenConns,
// MaxIdleConns, ...) apply to an HTTP client — don't need to implement it.
type openSearchDriver struct{}

func (d *openSearchDriver) Open(cfg DriverConfig) (*opensearchapi.Client, error) {
	return opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{cfg.DSN}},
	})
}

// startOpenSearchContainer starts a single-node OpenSearch container with
// security disabled and returns its address. Shared by this file and
// opensearch_research_test.go so both don't independently hand-roll (and
// risk drifting on) the same container/wait-strategy setup.
func startOpenSearchContainer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcopensearch.Run(ctx, "opensearchproject/opensearch:2.11.1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	addr, err := ctr.Address(ctx)
	require.NoError(t, err)
	return addr
}

type openSearchDocRepo struct {
	BaseRepo[*opensearchapi.Client]
	index string
}

func newOpenSearchDocRepo(exec *Executor[*opensearchapi.Client], index string) DocRepository {
	return &openSearchDocRepo{BaseRepo: NewBaseRepo("primary", exec), index: index}
}

func (r *openSearchDocRepo) Create(ctx context.Context, id, name string) error {
	return r.Run(ctx, func(ctx context.Context, c *opensearchapi.Client) error {
		body, err := json.Marshal(Doc{Name: name})
		if err != nil {
			return err
		}
		_, err = c.Document.Create(ctx, opensearchapi.DocumentCreateReq{
			Index:      r.index,
			DocumentID: id,
			Body:       bytes.NewReader(body),
		})
		return err
	})
}

func (r *openSearchDocRepo) FindByID(ctx context.Context, id string) (*Doc, error) {
	var doc Doc
	err := r.Run(ctx, func(ctx context.Context, c *opensearchapi.Client) error {
		resp, err := c.Document.Get(ctx, opensearchapi.DocumentGetReq{Index: r.index, DocumentID: id})
		if err != nil {
			return err
		}
		if !resp.Found {
			return fmt.Errorf("dbstore: document %q not found", id)
		}
		return json.Unmarshal(resp.Source, &doc)
	})
	doc.ID = id
	return &doc, err
}

// TestOpenSearchRepo_Pool_Lifecycle_And_DocumentRoundTrip proves the
// Pool[T]/Executor[T]/BaseRepo[T] generics work unmodified for a real
// non-SQL client: Register/Run/RemoveAll all behave correctly with
// T = *opensearchapi.Client, even though that client implements neither
// Closer nor PoolConfigApplier — both are optional capabilities, so their
// absence is a no-op rather than a compile or runtime error.
func TestOpenSearchRepo_Pool_Lifecycle_And_DocumentRoundTrip(t *testing.T) {
	ctx := context.Background()
	addr := startOpenSearchContainer(t)

	registry := NewDriverRegistry[*opensearchapi.Client]()
	registry.Register("opensearch", &openSearchDriver{})

	pool := NewPool(registry)
	require.NoError(t, pool.Register("primary", DriverConfig{
		Driver: "opensearch",
		DSN:    addr,
	}))

	exec := NewExecutor(pool)
	repo := newOpenSearchDocRepo(exec, "os_docs")

	require.NoError(t, repo.Create(ctx, "1", "Alice"))

	doc, err := repo.FindByID(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "Alice", doc.Name)
	assert.Equal(t, "1", doc.ID)

	// RemoveAll must succeed even though *opensearchapi.Client has no Close
	// method — closeClient's type assertion to Closer just no-ops for it.
	pool.RemoveAll()
}
