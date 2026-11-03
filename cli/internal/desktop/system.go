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

// DefaultURL is a placeholder until downloads are hosted.
const DefaultURL = "https://kstack.sh/download/{os}/{arch}"

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
	// Self is this executable. On Linux the app's binary is also named kstack,
	// so Locate must not take the CLI for the app.
	Self string

	HTTP *http.Client
	// Stall fails a download that receives nothing for this long, from the
	// request to the last byte; 0 means never.
	Stall time.Duration
	// URL is the installer's address, with {os} and {arch} for OS and Arch.
	URL string
	// TempDir holds each download's own directory; "" means os.TempDir.
	TempDir string

	// Run runs a command and waits for it to exit.
	Run func(ctx context.Context, name string, args ...string) error
	// Start starts a command and returns without waiting for it.
	Start func(name string, args ...string) error
}

// NewSystem returns the System for this machine.
func NewSystem() (*System, error) {
	home, err := os.UserHomeDir()
	if err != nil { // coverage:ignore every platform CI runs on sets a home directory
		return nil, err
	}
	self, err := os.Executable()
	if err != nil { // coverage:ignore every platform CI runs on reports the executable
		return nil, err
	}
	return &System{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		Root:   string(filepath.Separator),
		Home:   home,
		Getenv: os.Getenv,
		Self:   self,
		// No overall timeout: a large download takes as long as it needs.
		HTTP:  &http.Client{},
		Stall: 30 * time.Second,
		URL:   DefaultURL,
		Run:   run,
		Start: start,
	}, nil
}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out)))
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
