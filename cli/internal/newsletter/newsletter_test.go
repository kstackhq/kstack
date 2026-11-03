package newsletter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribePostsTheAddressAsJSON(t *testing.T) {
	t.Parallel()
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	c := Client{URL: srv.URL, HTTP: srv.Client()}
	require.NoError(t, c.Subscribe(context.Background(), "ada@example.com"))
	assert.Equal(t, map[string]string{"email": "ada@example.com"}, got)
}

func TestSubscribeFailsOnAnErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := Client{URL: srv.URL, HTTP: srv.Client()}
	require.EqualError(t, c.Subscribe(context.Background(), "ada@example.com"),
		"newsletter: the server answered 502 Bad Gateway")
}

func TestSubscribeFailsWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	c := Client{URL: srv.URL, HTTP: srv.Client()}
	require.Error(t, c.Subscribe(context.Background(), "ada@example.com"))
}

func TestSubscribeFailsOnABadURL(t *testing.T) {
	t.Parallel()
	c := Client{URL: "://", HTTP: http.DefaultClient}
	require.ErrorContains(t, c.Subscribe(context.Background(), "ada@example.com"), "missing protocol scheme")
}

func TestNewPointsAtTheDefaultEndpoint(t *testing.T) {
	t.Parallel()
	c := New()
	assert.Equal(t, "https://kstack.sh/api/newsletter", c.URL)
	assert.NotNil(t, c.HTTP)
}
