//go:build ignore

// Deletes the dev app's data and cache directories, so the next
// `pnpm tauri dev` starts from an empty database and resyncs its caches.
// Run from anywhere:
//
//	go run scripts/clean-dev-data.go   # `make clean-data`
//
// Each directory is resolved as src-tauri/src/paths.rs resolves it in a debug
// build. The leaf is always `Kstack-dev`, so an installed release's `Kstack`
// is never touched. It is Go rather than shell so it runs on Windows too.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const leaf = "Kstack-dev"

func main() {
	if err := clean(); err != nil {
		fmt.Fprintln(os.Stderr, "clean-dev-data:", err)
		os.Exit(1)
	}
}

func clean() error {
	data, cache, err := devDirs()
	if err != nil {
		return err
	}
	if err := refuseWhileRunning(data); err != nil {
		return err
	}
	// The cache first: on Windows it sits inside the data directory.
	for _, dir := range []string{cache, data} {
		if err := remove(dir); err != nil {
			return err
		}
	}
	return nil
}

// devDirs mirrors Tauri's local_data_dir and cache_dir, and paths.rs's
// cache_dir_for.
func devDirs() (data, cache string, err error) {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", "", errors.New("LOCALAPPDATA is not set")
		}
		data = filepath.Join(local, leaf)
		return data, filepath.Join(data, "cache"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", leaf),
			filepath.Join(home, "Library", "Caches", leaf), nil
	}
	return filepath.Join(xdg("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), leaf),
		filepath.Join(xdg("XDG_CACHE_HOME", filepath.Join(home, ".cache")), leaf), nil
}

// xdg reads an XDG base directory, which the spec ignores unless absolute.
func xdg(name, fallback string) string {
	if dir := os.Getenv(name); filepath.IsAbs(dir) {
		return dir
	}
	return fallback
}

// refuseWhileRunning fails when a dev sidecar holds the data directory open.
func refuseWhileRunning(data string) error {
	if runtime.GOOS == "windows" {
		return refuseWhileHeld(data)
	}
	return refuseWhileListening()
}

// refuseWhileListening looks for a live sidecar socket in the dev runtime
// directory. A socket a crashed run left behind refuses the dial, so it does
// not count.
func refuseWhileListening() error {
	for _, dir := range runtimeDirs() {
		socks, err := filepath.Glob(filepath.Join(dir, "kstack-sidecar-*.sock"))
		if err != nil {
			return err
		}
		for _, sock := range socks {
			conn, err := net.DialTimeout("unix", sock, time.Second)
			if err != nil {
				continue
			}
			conn.Close()
			return fmt.Errorf("a dev sidecar is listening on %s; quit `pnpm tauri dev` first", sock)
		}
	}
	return nil
}

// runtimeDirs mirrors paths.rs's runtime_dir: $XDG_RUNTIME_DIR/kstack-dev,
// else kstack-dev-<uid> under the temp dir, or a random sibling of it when
// that name was refused.
func runtimeDirs() []string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return []string{filepath.Join(dir, "kstack-dev")}
		}
	}
	base := filepath.Join(os.TempDir(), fmt.Sprintf("kstack-dev-%d", os.Geteuid()))
	siblings, _ := filepath.Glob(base + "-??????")
	return append([]string{base}, siblings...)
}

// refuseWhileHeld renames the data directory and back. Windows refuses to
// rename a directory while a file in it is open, and its sidecar listens on
// a named pipe rather than a socket under the runtime directory.
func refuseWhileHeld(data string) error {
	aside := data + ".clean"
	if err := os.Rename(data, aside); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s is in use; quit `pnpm tauri dev` first: %w", data, err)
	}
	return os.Rename(aside, data)
}

func remove(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("%s: already gone\n", dir)
		return nil
	} else if err != nil {
		return err
	}
	fmt.Printf("removing %s\n", dir)
	if os.RemoveAll(dir) == nil {
		return nil
	}
	// A chat's workspace can hold a read-only directory, whose entries cannot
	// be removed until it is writable again.
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
