package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// Run shows the TUI on in and out until the user quits or ctx ends. subscribe
// gets ctx, so cancelling the run cancels a submit in flight.
func Run(ctx context.Context, in io.Reader, out io.Writer, subscribe func(ctx context.Context, email string) error) error {
	m := New(func(email string) error { return subscribe(ctx, email) })
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}
