package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serve answers every request with status and body, recording each path.
func serve(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func downloadSystem(t *testing.T, goos string, srv *httptest.Server) *System {
	t.Helper()
	s := testSystem(t, goos, nil)
	s.HTTP = srv.Client()
	s.URL = srv.URL + "/download/{os}/{arch}"
	s.TempDir = t.TempDir()
	return s
}

func TestDownloadFetchesThePlatformsInstallerIntoADirOfItsOwn(t *testing.T) {
	t.Parallel()
	for goos, name := range map[string]string{
		"darwin":  "Kstack.dmg",
		"windows": "Kstack-setup.exe",
		"linux":   "Kstack.AppImage",
	} {
		srv, paths := serve(t, http.StatusOK, "installer bytes")
		s := downloadSystem(t, goos, srv)
		var last [2]int64
		installer, err := s.Download(context.Background(), func(done, total int64) { last = [2]int64{done, total} })
		require.NoError(t, err, goos)

		assert.Equal(t, []string{"/download/" + goos + "/arm64"}, *paths)
		assert.Equal(t, name, filepath.Base(installer))
		assert.Equal(t, s.TempDir, filepath.Dir(filepath.Dir(installer)))
		body, err := os.ReadFile(installer)
		require.NoError(t, err)
		assert.Equal(t, "installer bytes", string(body))
		assert.Equal(t, [2]int64{15, 15}, last)
	}
}

func TestDownloadFailsOnAnErrorStatusAndLeavesNothing(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, http.StatusNotFound, "")
	s := downloadSystem(t, "linux", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "download "+srv.URL+"/download/linux/arm64: the server answered 404 Not Found")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheBodyIsCutShortAndLeavesNothing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	s := downloadSystem(t, "linux", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "unexpected EOF")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, http.StatusOK, "")
	s := downloadSystem(t, "linux", srv)
	srv.Close()
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.Error(t, err)
}

func TestDownloadFailsOnABadURL(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, http.StatusOK, "")
	s := downloadSystem(t, "linux", srv)
	s.URL = "://"
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "missing protocol scheme")
}

func TestDownloadFailsWhenItCannotMakeItsDir(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, http.StatusOK, "")
	s := downloadSystem(t, "linux", srv)
	s.TempDir = filepath.Join(s.TempDir, "missing")
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.Error(t, err)
}

func TestDownloadRefusesAnUnsupportedOS(t *testing.T) {
	t.Parallel()
	srv, paths := serve(t, http.StatusOK, "")
	s := downloadSystem(t, "plan9", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "kstack app does not support plan9")
	assert.Empty(t, *paths)
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Empty(t, strings.Join(names, ", "))
}

// stallSystem downloads from handler, failing after stall with nothing arriving.
func stallSystem(t *testing.T, stall time.Duration, handler http.HandlerFunc) *System {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s := downloadSystem(t, "darwin", srv)
	s.Stall = stall
	return s
}

func TestDownloadFailsWhenNoAnswerArrives(t *testing.T) {
	t.Parallel()
	s := stallSystem(t, 20*time.Millisecond, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "nothing arrived for 20ms")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheBodyStalls(t *testing.T) {
	t.Parallel()
	s := stallSystem(t, 20*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("part"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "nothing arrived for 20ms")
	assertEmpty(t, s.TempDir)
}

func TestDownloadOutlastsTheStallWhileBytesArrive(t *testing.T) {
	t.Parallel()
	const stall = 200 * time.Millisecond
	s := stallSystem(t, stall, func(w http.ResponseWriter, _ *http.Request) {
		for range 6 {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			// Latency injected on purpose: the whole download outlasts the
			// stall, each gap does not.
			time.Sleep(stall / 4)
		}
	})
	installer, err := s.Download(context.Background(), func(int64, int64) {})
	require.NoError(t, err)
	body, err := os.ReadFile(installer)
	require.NoError(t, err)
	assert.Equal(t, "xxxxxx", string(body))
}
