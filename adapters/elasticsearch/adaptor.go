package elasticsearchadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"github.com/loykin/dbstore"
)

// Adaptor is the only handle a UserRepoTemplate-style backend
// implementation ever sees for an Elasticsearch source — it owns JSON
// marshaling and not-found translation so Template code never needs to
// import the elasticsearch client package directly. Unlike
// sqlxadapter.Adaptor, there is no WithTx: Elasticsearch has no
// transaction concept, so that capability simply isn't in this type's
// method set.
type Adaptor struct{ client *elasticsearch.Client }

// Index creates or replaces the document at index/id.
func (a Adaptor) Index(ctx context.Context, index, id string, doc any) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	resp, err := a.client.Index(index, bytes.NewReader(body), a.client.Index.WithDocumentID(id), a.client.Index.WithContext(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.IsError() {
		return fmt.Errorf("elasticsearchadapter: index %s/%s failed: %s", index, id, resp.Status())
	}
	return nil
}

// Get fetches the document at index/id and decodes it into dest. A 404 is
// translated into dbstore.ErrNotFound, which dbstore.Call turns into a
// (zero, nil) result for the caller.
func (a Adaptor) Get(ctx context.Context, index, id string, dest any) error {
	resp, err := a.client.Get(index, id, a.client.Get.WithContext(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return dbstore.ErrNotFound
	}
	if resp.IsError() {
		return fmt.Errorf("elasticsearchadapter: get %s/%s failed: %s", index, id, resp.Status())
	}

	var payload struct {
		Found  bool            `json:"found"`
		Source json.RawMessage `json:"_source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if !payload.Found {
		return dbstore.ErrNotFound
	}
	return json.Unmarshal(payload.Source, dest)
}

// Delete removes the document at index/id. A 404 is translated into
// dbstore.ErrNotFound the same way Get does.
func (a Adaptor) Delete(ctx context.Context, index, id string) error {
	resp, err := a.client.Delete(index, id, a.client.Delete.WithContext(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return dbstore.ErrNotFound
	}
	if resp.IsError() {
		return fmt.Errorf("elasticsearchadapter: delete %s/%s failed: %s", index, id, resp.Status())
	}
	return nil
}
