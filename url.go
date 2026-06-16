package daguito

import (
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"strings"
)

// DefaultAPIURL is the Daguito ingest gateway — the default base URL for the
// SDK/inbound surface (webhooks, streaming, knowledge search, audio, upload).
// Callers may override it via ClientOptions.APIURL or the matching field on the
// standalone helper inputs.
const DefaultAPIURL = "https://ingest.daguito.com"

// joinHTTP joins an HTTP base URL with a path, normalising slashes.
func joinHTTP(base, path string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// toWSURL rewrites an http(s):// base into ws(s):// and appends path + query.
// Used by WebhookStreamSession so callers configure a single apiUrl regardless
// of transport.
func toWSURL(apiURL, path string, query map[string]string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// randomSessionID returns a cryptographically-strong, URL-safe id with the
// given prefix. Mirrors the Python `random_session_id` helper.
func randomSessionID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Random source failure is unrecoverable; surface a deterministic
		// fallback so callers don't get an empty id. The session key is only
		// used to namespace this client's queue, not for auth.
		return prefix + "_fallback"
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b[:])
}
