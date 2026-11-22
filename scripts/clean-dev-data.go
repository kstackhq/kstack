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

// leaf is paths.rs's app_dir_name in a debug build: lowercase on Linux.
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
	dirs, err := devDirs()
	if err != nil {
		return err
	}
	if err := refuseWhileRunning(); err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := remove(dir); err != nil {
			return err
		}
	}
	return nil
}

// cleanWindowState deletes the dev app's saved window frame. It resolves the
// state directory alone, so another kind's unusable root does not stop it.
func cleanWindowState() error {
	state, err := devStateDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(state, windowStateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// devDirs is every folder paths.rs's layout gives a debug build. macOS and
// Windows keep several kinds in one app folder, which is listed as itself:
// were it a link, deleting a kind through it would empty the link's target.
func devDirs() ([]string, error) {
	switch runtime.GOOS {
	case "windows":
		app, err := devAppDir()
		if err != nil {
			return nil, err
		}
		roaming, err := windowsDir("APPDATA")
		if err != nil {
			return nil, err
		}
		// The config's folder, which is the app folder again when the
		// roaming root is the local one.
		return []string{app, filepath.Join(roaming, leaf)}, nil
	case "darwin":
		app, err := devAppDir()
		if err != nil {
			return nil, err
		}
		cache, err := underHome("Library", "Caches", leaf)
		if err != nil {
			return nil, err
		}
		logs, err := underHome("Library", "Logs", leaf)
		if err != nil {
			return nil, err
		}
		return []string{app, cache, logs}, nil
	}
	dataHome, err := xdg("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return nil, err
	}
	cacheHome, err := xdg("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return nil, err
	}
	configHome, err := xdg("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return nil, err
	}
	state, err := devStateDir()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(dataHome, leaf),
		filepath.Join(cacheHome, leaf),
		filepath.Join(configHome, leaf),
		state,
	}, nil
}

// devAppDir is the folder macOS and Windows keep the data and state in, with
// the config on macOS and the cache and runtime directory on Windows.
func devAppDir() (string, error) {
	if runtime.GOOS == "windows" {
		local, err := windowsDir("LOCALAPPDATA")
		if err != nil {
			return "", err
		}
		return filepath.Join(local, leaf), nil
	}
	return underHome("Library", "Application Support", leaf)
}

func devStateDir() (string, error) {
	if runtime.GOOS == "linux" {
		stateHome, err := xdg("XDG_STATE_HOME", ".local", "state")
		if err != nil {
			return "", err
		}
		return filepath.Join(stateHome, leaf), nil
	}
	app, err := devAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(app, "state"), nil
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

// refuseWhileRunning fails while a dev sidecar runs.
func refuseWhileRunning() error {
	if runtime.GOOS != "windows" {
		return refuseWhileListening()
	}
	app, err := devAppDir()
	if err != nil {
		return err
	}
	return refuseWhileHeld(app)
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

// refuseWhileHeld renames the app folder and back. Windows refuses to rename
// a directory while a file anywhere under it is open, and its sidecar listens
// on a named pipe rather than a socket under the runtime directory.
func refuseWhileHeld(app string) error {
	aside := app + ".clean"
	if err := os.Rename(app, aside); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s is in use; quit `pnpm tauri dev` first: %w", app, err)
	}
	return os.Rename(aside, app)
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
