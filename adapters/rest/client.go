package restadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is a small REST transport client: a base URL, an HTTP client, and
// default headers. Backend-specific paths, payloads, and response semantics
// belong to the repository or a higher-level backend adapter.
type Client struct {
	HTTPClient *http.Client
	BaseURL    *url.URL
	Header     http.Header
}

func (c *Client) NewRequest(ctx context.Context, method, requestPath string, body io.Reader) (*http.Request, error) {
	requestURL, err := c.resolve(requestPath)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, err
	}
	for key, values := range c.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return req, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return httpClient.Do(req)
}

func (c *Client) DoJSON(ctx context.Context, method, requestPath string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}

	req, err := c.NewRequest(ctx, method, requestPath, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("restadapter: %s %s returned %s: %s", method, requestPath, resp.Status, strings.TrimSpace(string(payload)))
	}
	if responseBody == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(responseBody)
}

func (c *Client) resolve(requestPath string) (*url.URL, error) {
	u, err := url.Parse(requestPath)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}

	rel := *u
	if strings.HasPrefix(requestPath, "/") {
		rel.Path = strings.TrimPrefix(u.Path, "/")
	}
	return c.BaseURL.ResolveReference(&rel), nil
}

func cloneHeader(header http.Header) http.Header {
	cloned := make(http.Header, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
