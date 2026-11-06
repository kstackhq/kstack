// Package cmd builds the kstack CLI's commands. What each one does is
// passed in, so tests run the commands without a terminal, a network or an app.
package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Deps is what the commands run.
type Deps struct {
	// TUI runs the terminal UI until the user quits.
	TUI func(ctx context.Context) error
	// App opens the desktop app, installing it first if it is missing, and
	// reports its progress to out.
	App func(ctx context.Context, out io.Writer) error
	// Version is what `kstack --version` prints.
	Version string
}

// Execute runs `kstack args...` with d and returns the process's exit status.
// An interrupt exits without a message, since the user asked for it.
func Execute(ctx context.Context, d Deps, args []string, stdout, stderr io.Writer) int {
	root := New(d)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if ctx.Err() == nil {
		fmt.Fprintln(stderr, "Error:", err)
	}
	return 1
}

// New returns the root command, `kstack`, with its subcommands. Execute
// prints an error; the command does not.
func New(d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:   "kstack",
		Short: "Kstack from the terminal",
		Long: "Kstack from the terminal.\n\n" +
			"Run alone, kstack opens a placeholder terminal UI where you can sign up\n" +
			"for the newsletter while the real one is built.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       d.Version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return d.TUI(cmd.Context())
		},
	}
	root.AddCommand(newAppCmd(d))
	return root
}
