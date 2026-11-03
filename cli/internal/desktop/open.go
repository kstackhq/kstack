// Package desktop finds, installs and launches the Kstack desktop app.
package desktop

import (
	"context"
	"fmt"
	"io"
)

// Steps are the platform's parts of opening the app. Open decides which run;
// System is the real implementation, and tests drive Open with a fake.
type Steps interface {
	// Locate returns the installed app's path, or "" when it is not installed.
	Locate() (string, error)
	// Download fetches the installer, reporting bytes done of total (-1 when
	// the size is unknown), and returns its path.
	Download(ctx context.Context, progress func(done, total int64)) (string, error)
	// Install runs or places the installer, removes it, and returns the
	// installed app's path.
	Install(ctx context.Context, installer string) (string, error)
	// Launch starts the app at path and returns without waiting for it.
	Launch(path string) error
}

// Open launches the installed app, first downloading and installing it when
// it is missing. It reports each step to out.
func Open(ctx context.Context, s Steps, out io.Writer) error {
	path, err := s.Locate()
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Fprintln(out, "Kstack is not installed. Downloading it...")
		installer, err := s.Download(ctx, progress(out))
		fmt.Fprintln(out)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Installing...")
		if path, err = s.Install(ctx, installer); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Opening %s\n", path)
	return s.Launch(path)
}
