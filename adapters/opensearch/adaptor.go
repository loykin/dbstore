package opensearchadapter

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
)

// Adaptor is the only handle a UserRepoTemplate-style backend
// implementation ever sees for an OpenSearch source — it owns JSON
// marshaling and not-found translation so Template code never needs to
// import opensearchapi. Unlike sqlxadapter.Adaptor, there is no WithTx:
// OpenSearch has no transaction concept, so that capability simply isn't
// in this type's method set.
type Adaptor struct{ client *opensearchapi.Client }

// Index creates or replaces the document at index/id.
func (a Adaptor) Index(ctx context.Context, index, id string, doc any) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = a.client.Index(ctx, opensearchapi.IndexReq{
		Index:      index,
		DocumentID: id,
		Body:       bytes.NewReader(body),
	})
	return err
}

// Get fetches the document at index/id and decodes it into dest. A missing
// document is translated into dbstore.ErrNotFound, which dbstore.Call turns
// into a (zero, nil) result for the caller.
func (a Adaptor) Get(ctx context.Context, index, id string, dest any) error {
	resp, err := a.client.Document.Get(ctx, opensearchapi.DocumentGetReq{
		Index:      index,
		DocumentID: id,
	})
	if err != nil {
		return err
	}
	if !resp.Found {
		return dbstore.ErrNotFound
	}
	return json.Unmarshal(resp.Source, dest)
}

// Delete removes the document at index/id.
func (a Adaptor) Delete(ctx context.Context, index, id string) error {
	_, err := a.client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{
		Index:      index,
		DocumentID: id,
	})
	return err
}
