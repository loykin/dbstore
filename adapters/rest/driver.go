package restadapter

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/loykin/dbstore"
)

// DriverBuilder is the REST driver contract expected by Adapter.RegisterDriver.
// Applications may implement their own for backend-specific auth or
// transport needs; Driver below covers the common case.
type DriverBuilder = dbstore.DriverBuilder[*Client]

// Driver opens a Client whose base URL is cfg.DSN. Unlike SQL dialects,
// net/http needs no backend-specific low-level driver import, so — unlike
// sqlxadapter, which must leave dialect drivers to the application — this
// default covers any REST endpoint without pulling in extra dependencies.
// HTTPClient and Header are optional overrides shared by every source
// opened with this Driver value.
type Driver struct {
	HTTPClient *http.Client
	Header     http.Header
}

func (d Driver) Open(cfg dbstore.SourceConfig) (*Client, error) {
	baseURL, err := url.Parse(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("restadapter: dsn must be an absolute URL")
	}
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}

	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		Header:     cloneHeader(d.Header),
	}, nil
}
