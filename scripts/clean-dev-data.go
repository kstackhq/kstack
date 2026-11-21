//go:build ignore

// Deletes the dev app's data, cache, config and state directories, so the
// next `pnpm tauri dev` starts from an empty database and default settings,
// and resyncs its caches.
// With -window-state it deletes only the saved window frame, so the next dev
// launch opens at the default size. Run from anywhere:
//
//	go run scripts/clean-dev-data.go                 # `make clean-data`
//	go run scripts/clean-dev-data.go -window-state   # part of `make clean`
//
// Each directory is resolved as src-tauri/src/paths.rs resolves it in a debug
// build. The leaf always ends in `-dev`, so an installed release's folders are
// never touched. It is Go rather than shell so it runs on Windows too.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// leaf is paths.rs's APP_DIR_NAME in a debug build: lowercase on Linux.
var leaf = devLeaf()

func devLeaf() string {
	if runtime.GOOS == "linux" {
		return "kstack-dev"
	}
	return "Kstack-dev"
}

// windowStateFile is src-tauri/src/window_state.rs's FILE_NAME.
const windowStateFile = "window-state.json"

func main() {
	windowState := flag.Bool("window-state", false, "delete only the saved window frame")
	flag.Parse()
	run := clean
	if *windowState {
		run = cleanWindowState
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "clean-dev-data:", err)
		os.Exit(1)
	}
}

func clean() error {
	data, err := devDataDir()
	if err != nil {
		return err
	}
	cache, err := devCacheDir(data)
	if err != nil {
		return err
	}
	config, err := devConfigDir()
	if err != nil {
		return err
	}
	state, err := devStateDir(data)
	if err != nil {
		return err
	}
	if err := refuseWhileRunning(data); err != nil {
		return err
	}
	// The cache first: on Windows it sits inside the data directory.
	dirs := []string{cache, data}
	for _, dir := range []string{config, state} {
		if dir != data {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		if err := remove(dir); err != nil {
			return err
		}
	}
	return nil
}

// cleanWindowState deletes the dev app's saved window frame.
func cleanWindowState() error {
	data, err := devDataDir()
	if err != nil {
		return err
	}
	state, err := devStateDir(data)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(state, windowStateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// devDataDir mirrors Tauri's local_data_dir.
func devDataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		local, err := windowsDir("LOCALAPPDATA")
		if err != nil {
			return "", err
		}
		return filepath.Join(local, leaf), nil
	case "darwin":
		return underHome("Library", "Application Support", leaf)
	}
	dataHome, err := xdg("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, leaf), nil
}

// devCacheDir mirrors Tauri's cache_dir and paths.rs's cache_dir_for.
func devCacheDir(data string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(data, "cache"), nil
	case "darwin":
		return underHome("Library", "Caches", leaf)
	}
	cacheHome, err := xdg("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheHome, leaf), nil
}

// devStateDir mirrors paths.rs's app_state_dir: a leaf on XDG's state root on
// Linux, the data directory elsewhere.
func devStateDir(data string) (string, error) {
	if runtime.GOOS != "linux" {
		return data, nil
	}
	stateHome, err := xdg("XDG_STATE_HOME", ".local", "state")
	if err != nil {
		return "", err
	}
	return filepath.Join(stateHome, leaf), nil
}

// devConfigDir mirrors Tauri's config_dir and paths.rs's app_config_dir,
// where host.json lives. On macOS it is the data directory.
func devConfigDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		roaming, err := windowsDir("APPDATA")
		if err != nil {
			return "", err
		}
		return filepath.Join(roaming, leaf), nil
	case "darwin":
		return underHome("Library", "Application Support", leaf)
	}
	configHome, err := xdg("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, leaf), nil
}

// windowsDir reads a Windows profile folder from the environment, refusing a
// relative value, which would delete a folder under the working directory.
func windowsDir(name string) (string, error) {
	dir := os.Getenv(name)
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%s is %q, not an absolute path", name, dir)
	}
	return dir, nil
}

// underHome joins elems onto $HOME, refused when it holds `..` (noDotDot).
func underHome(elems ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if err := noDotDot("HOME", home); err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, elems...)...), nil
}

// xdg reads an XDG base directory, which the spec ignores unless absolute,
// and falls back to elems under $HOME. $HOME is read only for the fallback.
func xdg(name string, elems ...string) (string, error) {
	if dir := os.Getenv(name); filepath.IsAbs(dir) {
		return dir, noDotDot(name, dir)
	}
	return underHome(elems...)
}

// noDotDot refuses a Unix base directory holding `..`, as the app does. Go
// joins it by name, while the filesystem reads a `..` after a link as the
// link target's parent, so the two could name different folders. It is
// checked before any join, which would drop the `..`.
func noDotDot(name, dir string) error {
	if slices.Contains(strings.Split(dir, "/"), "..") {
		return fmt.Errorf("%s is %q, which holds `..`; set it without one", name, dir)
	}
	return nil
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
	dirs, err := runtimeDirs()
	if err != nil {
		return err
	}
	for _, dir := range dirs {
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
func runtimeDirs() ([]string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		// Before the stat: a link the host resolved through at launch may be
		// gone, and the temp dir would then be searched instead.
		if err := noDotDot("XDG_RUNTIME_DIR", dir); err != nil {
			return nil, err
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return []string{filepath.Join(dir, "kstack-dev")}, nil
		}
	}
	temp := os.TempDir()
	if err := noDotDot("TMPDIR", temp); err != nil {
		return nil, err
	}
	base := filepath.Join(temp, fmt.Sprintf("kstack-dev-%d", os.Geteuid()))
	siblings, _ := filepath.Glob(base + "-??????")
	return append([]string{base}, siblings...), nil
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
