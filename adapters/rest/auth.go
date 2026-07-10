package restadapter

import (
	"encoding/base64"
	"net/http"
)

// BasicAuth returns an http.Header carrying HTTP Basic authentication for
// credentials that don't rotate. It is the static-header counterpart to
// Driver.HTTPClient, which is where a custom http.RoundTripper plugs in for
// auth that must be computed per request or refreshed over time (OAuth2
// token refresh, request signing, mTLS, ...).
func BasicAuth(username, password string) http.Header {
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return http.Header{"Authorization": []string{"Basic " + token}}
}
