package desktop

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultReleases lists Kstack's GitHub releases, newest first.
const DefaultReleases = "https://api.github.com/repos/kstackhq/kstack/releases?per_page=100"

// System is Steps on this machine. It switches on OS rather than on build
// tags, so every platform's choices are tested on any platform.
type System struct {
	OS   string // a GOOS
	Arch string // a GOARCH
	// Root prefixes the system-wide folders: "/" outside tests. Windows names
	// its folders through the environment instead.
	Root   string
	Home   string
	Getenv func(string) string
	// IsRoot is true when this process runs as root, so a Linux package
	// installs without sudo.
	IsRoot bool

	HTTP *http.Client
	// Stall fails a download that receives nothing for this long, from the
	// request to the last byte; 0 means never.
	Stall time.Duration
	// Releases is the GitHub API's list of releases Download picks from.
	Releases string
	// TempDir holds each download's own directory; "" means os.TempDir.
	TempDir string

	// Run runs a command and waits for it to exit.
	Run func(ctx context.Context, name string, args ...string) error
	// Interact runs a command on this terminal and waits for it to exit.
	Interact func(ctx context.Context, name string, args ...string) error
	// Start starts a command and returns without waiting for it.
	Start func(name string, args ...string) error
}

// NewSystem returns the System for this machine.
func NewSystem() (*System, error) {
	home, err := os.UserHomeDir()
	if err != nil { // coverage:ignore every platform CI runs on sets a home directory
		return nil, err
	}
	return &System{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		Root:   string(filepath.Separator),
		Home:   home,
		Getenv: os.Getenv,
		IsRoot: os.Geteuid() == 0,
		// No overall timeout: a large download takes as long as it needs.
		HTTP:     &http.Client{},
		Stall:    30 * time.Second,
		Releases: DefaultReleases,
		Run:      run,
		Interact: interact,
		Start:    start,
	}, nil
}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func interact(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}

func start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Launch starts the app at path, detached from this process.
func (s *System) Launch(path string) error {
	if s.OS == "darwin" {
		return s.Start("open", "-a", path)
	}
	return s.Start(path)
}

func (s *System) unsupported() error {
	return fmt.Errorf("kstack app does not support %s", s.OS)
}
