package restadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/loykin/dbstore"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type testDriver struct {
	HTTPClient *http.Client
	Header     http.Header
}

func (d testDriver) Open(cfg dbstore.SourceConfig) (*Client, error) {
	baseURL, err := url.Parse(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("test: DSN must be an absolute URL")
	}
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}
	return &Client{
		HTTPClient: d.HTTPClient,
		BaseURL:    baseURL,
		Header:     cloneHeader(d.Header),
	}, nil
}

func TestClient_DoJSON(t *testing.T) {
	var gotPath string
	driver := testDriver{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotPath = req.URL.Path
				if req.Header.Get("X-Test") != "yes" {
					t.Fatalf("X-Test header = %q, want yes", req.Header.Get("X-Test"))
				}
				var payload map[string]string
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["name"] != "Alice" {
					t.Fatalf("name = %q, want Alice", payload["name"])
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"Alice"}`)),
					Request:    req,
				}, nil
			}),
		},
		Header: http.Header{"X-Test": []string{"yes"}},
	}

	client, err := driver.Open(dbstore.SourceConfig{DSN: "https://example.test/api"})
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := client.DoJSON(context.Background(), http.MethodPut, "/users/1", map[string]string{"name": "Alice"}, &response); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/users/1" {
		t.Fatalf("path = %q, want /api/users/1", gotPath)
	}
	if response.ID != "1" || response.Name != "Alice" {
		t.Fatalf("response = %+v", response)
	}
}

func TestSource_Run(t *testing.T) {
	adapter := New()
	adapter.RegisterDriver("rest", testDriver{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Status:     "204 No Content",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}),
		},
	})
	defer adapter.Close()

	if err := adapter.Open("api", dbstore.SourceConfig{Driver: "rest", DSN: "https://example.test"}); err != nil {
		t.Fatal(err)
	}

	source := NewSource("api", adapter.Executor())
	if err := source.Run(context.Background(), func(ctx context.Context, client *Client) error {
		return client.DoJSON(ctx, http.MethodDelete, "/documents/1", nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
}
