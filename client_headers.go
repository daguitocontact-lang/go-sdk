package daguito

import (
	"net/http"
	"net/url"
)

// clientHeaderValue is the legible UA-like value sent in X-Daguito-Client.
func clientHeaderValue() string {
	return "daguito-sdk-go/" + Version
}

// applyClientHeaders adds the SDK identity headers to h. Safe to call
// after Authorization/Content-Type are set — non-overlapping keys.
func applyClientHeaders(h http.Header) {
	h.Set("X-Daguito-Client", clientHeaderValue())
	h.Set("X-Daguito-Client-Lang", sdkLang)
	h.Set("X-Daguito-Client-Version", Version)
}

// appendClientQueryParams returns rawURL with x_daguito_client_lang and
// x_daguito_client_version query params appended (or merged with existing
// query). Used for WebSocket upgrades where intermediate proxies may strip
// custom headers.
func appendClientQueryParams(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, err
	}
	q := parsed.Query()
	q.Set("x_daguito_client_lang", sdkLang)
	q.Set("x_daguito_client_version", Version)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}
