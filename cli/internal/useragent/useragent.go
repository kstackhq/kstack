// Package useragent names the CLI on every request it sends.
package useragent

import (
	"net/http"
	"runtime"
)

// String is the CLI's User-Agent, in client-go's shape:
// kstack-cli/<version> (<GOOS>/<GOARCH>).
func String(version string) string {
	return "kstack-cli/" + version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

// Client is the CLI's one HTTP client, setting String(version) on every request.
func Client(version string) *http.Client {
	return &http.Client{Transport: transport{userAgent: String(version)}}
}

// transport sends through http.DefaultTransport.
type transport struct{ userAgent string }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	// A RoundTripper may not change the caller's request.
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", t.userAgent)
	return http.DefaultTransport.RoundTrip(r)
}
