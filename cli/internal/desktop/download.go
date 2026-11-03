package desktop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// installers names each platform's installer: a disk image on macOS, the NSIS
// setup on Windows, an AppImage on Linux.
var installers = map[string]string{
	"darwin":  "Kstack.dmg",
	"windows": "Kstack-setup.exe",
	"linux":   "Kstack.AppImage",
}

// installerDir prefixes the directory each download gets of its own.
const installerDir = "kstack-install-"

// Download fetches this platform's installer into a new directory under
// TempDir. Install removes that directory; a failed download removes it here.
func (s *System) Download(ctx context.Context, progress func(done, total int64)) (string, error) {
	name, ok := installers[s.OS]
	if !ok {
		return "", s.unsupported()
	}
	url := strings.NewReplacer("{os}", s.OS, "{arch}", s.Arch).Replace(s.URL)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	arrived := func() {}
	if s.Stall > 0 {
		stalled := time.AfterFunc(s.Stall, func() {
			cancel(fmt.Errorf("download %s: nothing arrived for %s", url, s.Stall))
		})
		defer stalled.Stop()
		arrived = func() { stalled.Reset(s.Stall) }
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", causeOf(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: the server answered %s", url, resp.Status)
	}

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
