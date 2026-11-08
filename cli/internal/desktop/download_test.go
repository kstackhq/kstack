package desktop

import (
	"context"
	"encoding/json"
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

// testRelease is a release as GitHub lists it, its assets served under /files/.
type testRelease struct {
	tag    string
	draft  bool
	assets []string
}

// releaseServer lists releases at /releases and answers /files/<name> with
// file, recording each path asked for.
func releaseServer(t *testing.T, file http.HandlerFunc, releases ...testRelease) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/releases" {
			file(w, r)
			return
		}
		list := []map[string]any{}
		for _, rel := range releases {
			assets := []map[string]string{}
			for _, name := range rel.assets {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + "/files/" + name})
			}
			list = append(list, map[string]any{"tag_name": rel.tag, "draft": rel.draft, "assets": assets})
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// answer writes body for every file.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func downloadSystem(t *testing.T, goos string, srv *httptest.Server) *System {
	t.Helper()
	s := testSystem(t, goos, nil)
	s.HTTP = srv.Client()
	s.Releases = srv.URL + "/releases"
	s.TempDir = t.TempDir()
	if goos == "linux" {
		touch(t, filepath.Join(s.Root, "usr", "bin", "apt-get"), "")
	}
	return s
}

// everyAsset is every installer a 1.2.0 release attaches.
var everyAsset = []string{
	"kstack-desktop-1.2.0-linux-amd64.deb",
	"kstack-desktop-1.2.0-linux-arm64.deb",
	"kstack-desktop-1.2.0-linux-amd64.rpm",
	"kstack-desktop-1.2.0-linux-arm64.rpm",
	"kstack-desktop-1.2.0-darwin-universal.dmg",
	"kstack-desktop-1.2.0-windows-amd64.msi",
	"kstack-desktop-1.2.0-windows-arm64.msi",
}

func TestDownloadFetchesThePlatformsFileFromTheNewestDesktopRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos, manager, want string
	}{
		{"darwin", "", "kstack-desktop-1.2.0-darwin-universal.dmg"},
		{"windows", "", "kstack-desktop-1.2.0-windows-arm64.msi"},
		{"linux", "apt-get", "kstack-desktop-1.2.0-linux-arm64.deb"},
		{"linux", "dnf", "kstack-desktop-1.2.0-linux-arm64.rpm"},
	} {
		srv, paths := releaseServer(t, answer("installer bytes"),
			testRelease{tag: "cli/v9.0.0", assets: []string{"kstack-cli-9.0.0-linux-arm64.tar.gz"}},
			testRelease{tag: "desktop/v2.0.0", draft: true},
			testRelease{tag: "desktop/v1.2.0", assets: everyAsset},
			testRelease{tag: "desktop/v1.1.0", assets: everyAsset},
		)
		s := downloadSystem(t, tc.goos, srv)
		if tc.manager != "" {
			require.NoError(t, os.RemoveAll(filepath.Join(s.Root, "usr", "bin")))
			touch(t, filepath.Join(s.Root, "usr", "bin", tc.manager), "")
		}
		var last [2]int64
		installer, err := s.Download(context.Background(), func(done, total int64) { last = [2]int64{done, total} })
		require.NoError(t, err, tc.want)

		assert.Equal(t, []string{"/releases", "/files/" + tc.want}, *paths)
		assert.Equal(t, tc.want, filepath.Base(installer))
		assert.Equal(t, s.TempDir, filepath.Dir(filepath.Dir(installer)))
		body, err := os.ReadFile(installer)
		require.NoError(t, err)
		assert.Equal(t, "installer bytes", string(body))
		assert.Equal(t, [2]int64{15, 15}, last)
	}
}

func TestDownloadOnLinuxPrefersAptGetOverDnf(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, answer(""), testRelease{tag: "desktop/v1.2.0", assets: everyAsset})
	s := downloadSystem(t, "linux", srv)
	touch(t, filepath.Join(s.Root, "usr", "bin", "dnf"), "")
	installer, err := s.Download(context.Background(), func(int64, int64) {})
	require.NoError(t, err)
	assert.Equal(t, "kstack-desktop-1.2.0-linux-arm64.deb", filepath.Base(installer))
}

func TestDownloadOnLinuxNeedsAptGetOrDnf(t *testing.T) {
	t.Parallel()
	srv, paths := releaseServer(t, answer(""))
	s := downloadSystem(t, "linux", srv)
	require.NoError(t, os.RemoveAll(filepath.Join(s.Root, "usr", "bin")))
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "found neither")
	assert.Empty(t, *paths)
}

func TestDownloadFailsWhenTheNewestReleaseLacksThePlatformsFile(t *testing.T) {
	t.Parallel()
	srv, paths := releaseServer(t, answer(""),
		testRelease{tag: "desktop/v1.3.0", assets: []string{"kstack-desktop-1.3.0-darwin-universal.dmg"}},
		testRelease{tag: "desktop/v1.2.0", assets: everyAsset},
	)
	s := downloadSystem(t, "linux", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "release desktop/v1.3.0 has no kstack-desktop-1.3.0-linux-arm64.deb")
	assert.Equal(t, []string{"/releases"}, *paths)
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWithoutADesktopRelease(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, answer(""), testRelease{tag: "cli/v1.0.0"}, testRelease{tag: "desktop/v1.0.0", draft: true})
	s := downloadSystem(t, "darwin", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, s.Releases+" lists no desktop release")
}

func TestDownloadFailsOnAListItCannotRead(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(answer("not json"))
	t.Cleanup(srv.Close)
	s := downloadSystem(t, "darwin", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "read "+s.Releases+": invalid character")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheListAnswersAnError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	s := downloadSystem(t, "darwin", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "download "+s.Releases+": the server answered 404 Not Found")
}

func TestDownloadFailsWhenTheFileAnswersAnErrorAndLeavesNothing(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, http.NotFound, testRelease{tag: "desktop/v1.2.0", assets: everyAsset})
	s := downloadSystem(t, "darwin", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "download "+srv.URL+"/files/kstack-desktop-1.2.0-darwin-universal.dmg: the server answered 404 Not Found")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheBodyIsCutShortAndLeavesNothing(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}, testRelease{tag: "desktop/v1.2.0", assets: everyAsset})
	s := downloadSystem(t, "linux", srv)
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "unexpected EOF")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, answer(""))
	s := downloadSystem(t, "linux", srv)
	srv.Close()
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.Error(t, err)
}

func TestDownloadFailsOnABadURL(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, answer(""))
	s := downloadSystem(t, "linux", srv)
	s.Releases = "://"
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.ErrorContains(t, err, "missing protocol scheme")
}

func TestDownloadFailsWhenItCannotMakeItsDir(t *testing.T) {
	t.Parallel()
	srv, _ := releaseServer(t, answer(""), testRelease{tag: "desktop/v1.2.0", assets: everyAsset})
	s := downloadSystem(t, "linux", srv)
	s.TempDir = filepath.Join(s.TempDir, "missing")
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.Error(t, err)
}

func TestDownloadRefusesAnUnsupportedOS(t *testing.T) {
	t.Parallel()
	srv, paths := releaseServer(t, answer(""))
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

// stallSystem downloads its file from file, failing after stall with nothing
// arriving.
func stallSystem(t *testing.T, stall time.Duration, file http.HandlerFunc) *System {
	t.Helper()
	srv, _ := releaseServer(t, file, testRelease{tag: "desktop/v1.2.0", assets: everyAsset})
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
	require.EqualError(t, err, "download: nothing arrived for 20ms")
	assertEmpty(t, s.TempDir)
}

func TestDownloadFailsWhenTheListStalls(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	s := downloadSystem(t, "darwin", srv)
	s.Stall = 20 * time.Millisecond
	_, err := s.Download(context.Background(), func(int64, int64) {})
	require.EqualError(t, err, "download: nothing arrived for 20ms")
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
