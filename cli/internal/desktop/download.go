package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// installerDir prefixes the directory each download gets of its own.
const installerDir = "kstack-install-"

// Download fetches this platform's file from the newest desktop release into
// a new directory under TempDir. Install removes that directory; a failed
// download removes it here.
func (s *System) Download(ctx context.Context, progress func(done, total int64)) (string, error) {
	suffix, err := s.assetSuffix()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	arrived := func() {}
	if s.Stall > 0 {
		stalled := time.AfterFunc(s.Stall, func() {
			cancel(fmt.Errorf("download: nothing arrived for %s", s.Stall))
		})
		defer stalled.Stop()
		arrived = func() { stalled.Reset(s.Stall) }
	}

	name, url, err := s.findAsset(ctx, suffix)
	if err != nil {
		return "", causeOf(ctx, err)
	}
	arrived()
	resp, err := s.get(ctx, url)
	if err != nil {
		return "", causeOf(ctx, err)
	}
	defer resp.Body.Close()

	dir, err := os.MkdirTemp(s.TempDir, installerDir)
	if err != nil {
		return "", err
	}
	installer := filepath.Join(dir, name)
	body := &progressReader{r: resp.Body, total: resp.ContentLength, progress: func(done, total int64) {
		arrived()
		progress(done, total)
	}}
	if err := writeFile(installer, body); err != nil {
		os.RemoveAll(dir)
		return "", causeOf(ctx, err)
	}
	return installer, nil
}

// assetSuffix is what follows kstack-desktop-<version> in this platform's
// release file: the universal disk image on macOS, the MSI on Windows, and on
// Linux the .deb when apt-get is installed, else the .rpm when dnf is.
func (s *System) assetSuffix() (string, error) {
	switch s.OS {
	case "darwin":
		return "-darwin-universal.dmg", nil
	case "windows":
		return "-windows-" + s.Arch + ".msi", nil
	case "linux":
		switch {
		case s.hasTool("apt-get"):
			return "-linux-" + s.Arch + ".deb", nil
		case s.hasTool("dnf"):
			return "-linux-" + s.Arch + ".rpm", nil
		}
		return "", errors.New("kstack app installs Kstack with apt-get or dnf, and found neither; " +
			"download the .deb or .rpm from https://github.com/kstackhq/kstack/releases")
	}
	return "", s.unsupported()
}

// hasTool reports whether /usr/bin/<name> is a program.
func (s *System) hasTool(name string) bool {
	return isExecutable(filepath.Join(s.Root, "usr", "bin", name))
}

// release is the part of a GitHub release Download reads.
type release struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// findAsset returns the name and address of the newest desktop release's
// file ending in suffix. Pre-releases count; drafts do not. GitHub lists the
// newest release first.
func (s *System) findAsset(ctx context.Context, suffix string) (name, url string, err error) {
	resp, err := s.get(ctx, s.Releases)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", "", fmt.Errorf("read %s: %w", s.Releases, err)
	}
	for _, r := range releases {
		version, ok := strings.CutPrefix(r.TagName, "desktop/v")
		if !ok || r.Draft {
			continue
		}
		name := "kstack-desktop-" + version + suffix
		for _, a := range r.Assets {
			if a.Name == name {
				return name, a.URL, nil
			}
		}
		return "", "", fmt.Errorf("release %s has no %s", r.TagName, name)
	}
	return "", "", fmt.Errorf("%s lists no desktop release", s.Releases)
}

// get requests url and fails on any status but 200 OK.
func (s *System) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: the server answered %s", url, resp.Status)
	}
	return resp, nil
}

// causeOf returns why ctx ended, a stall's message included, or err while
// ctx is live.
func causeOf(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

// writeFile creates path, or truncates it, and copies r into it.
func writeFile(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// progressReader reports the bytes read through it.
type progressReader struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if n > 0 {
		p.progress(p.done, p.total)
	}
	return n, err
}
