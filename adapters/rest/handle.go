package restadapter

import (
	"context"
	"errors"
	"net/http"

	"github.com/loykin/dbstore"
)

// Handle is the only handle a UserRepoBackend-style backend
// implementation ever sees for a REST source — it owns HTTP-status-to-
// not-found translation so Backend code never needs to inspect a
// *StatusError itself. Unlike sqlxadapter.Handle, there is no WithTx: REST
// generally can't offer transactions, so that capability simply isn't in
// this type's method set — a Backend can't fake atomicity it doesn't have.
type Handle struct{ c *Client }

// Get issues a GET request and decodes the JSON response into dest. A 404
// is translated into dbstore.ErrNotFound, which generated repositories
// preserve for the caller.
func (a Handle) Get(ctx context.Context, path string, dest any) error {
	err := a.c.DoJSON(ctx, http.MethodGet, path, nil, dest)
	if isNotFound(err) {
		return dbstore.ErrNotFound
	}
	return err
}

// Post issues a POST request with a JSON body and discards the response.
func (a Handle) Post(ctx context.Context, path string, body any) error {
	return a.c.DoJSON(ctx, http.MethodPost, path, body, nil)
}

// Put issues a PUT request with a JSON body and discards the response.
func (a Handle) Put(ctx context.Context, path string, body any) error {
	return a.c.DoJSON(ctx, http.MethodPut, path, body, nil)
}

// Delete issues a DELETE request. A 404 is translated into
// dbstore.ErrNotFound the same way Get does.
func (a Handle) Delete(ctx context.Context, path string) error {
	err := a.c.DoJSON(ctx, http.MethodDelete, path, nil, nil)
	if isNotFound(err) {
		return dbstore.ErrNotFound
	}
	return err
}

func isNotFound(err error) bool {
	var statusErr *StatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound
}
