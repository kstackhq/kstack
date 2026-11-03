// Command kstack is Kstack's CLI: `kstack app` opens the desktop app, and
// `kstack` alone opens the terminal UI.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/kstackhq/kstack/cli/cmd"
	"github.com/kstackhq/kstack/cli/internal/desktop"
	"github.com/kstackhq/kstack/cli/internal/newsletter"
	"github.com/kstackhq/kstack/cli/internal/tui"
)

func main() { // coverage:ignore only wires the real dependencies, which no test runs
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	deps := cmd.Deps{
		TUI: func(ctx context.Context) error {
			return tui.Run(ctx, os.Stdin, os.Stdout, newsletter.New().Subscribe)
		},
		App: func(ctx context.Context, out io.Writer) error {
			s, err := desktop.NewSystem()
			if err != nil {
				return err
			}
			return desktop.Open(ctx, s, out)
		},
	}
	status := cmd.Execute(ctx, deps, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(status)
}
