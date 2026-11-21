package useragent

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringNamesTheCLIAndThePlatform(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "kstack-cli/0.3.0-rc.1 ("+runtime.GOOS+"/"+runtime.GOARCH+")", String("0.3.0-rc.1"))
}

func TestClientNamesTheCLIOnEveryRequest(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := Client("dev").Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, String("dev"), got)
	assert.Empty(t, req.Header.Get("User-Agent"), "the caller's request is untouched")
}
