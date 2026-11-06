package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execute runs the CLI with args and returns what it printed and its status.
func execute(t *testing.T, d Deps, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	status := Execute(context.Background(), d, args, &out, &out)
	return out.String(), status
}

func TestHelpDescribesKstackAndListsApp(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{}, "--help")
	require.Zero(t, status)
	assert.Contains(t, out, "opens a placeholder terminal UI")
	assert.Contains(t, out, "app         Open the Kstack desktop app, installing it if it is missing")
}

func TestVersionPrintsTheBuildsVersion(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{Version: "1.2.3-rc.1"}, "--version")
	require.Zero(t, status)
	assert.Equal(t, "kstack version 1.2.3-rc.1\n", out)
}

func TestKstackAloneRunsTheTUI(t *testing.T) {
	t.Parallel()
	ran := false
	_, status := execute(t, Deps{TUI: func(context.Context) error { ran = true; return nil }})
	require.Zero(t, status)
	assert.True(t, ran)
}

func TestACommandsErrorIsPrintedWithoutTheUsage(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{App: func(context.Context, io.Writer) error { return errors.New("no network") }}, "app")
	assert.Equal(t, 1, status)
	assert.Equal(t, "Error: no network\n", out)
}

func TestAnInterruptExitsWithoutAMessage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	status := Execute(ctx, Deps{App: func(ctx context.Context, _ io.Writer) error {
		cancel()
		return ctx.Err()
	}}, []string{"app"}, &out, &out)
	assert.Equal(t, 1, status)
	assert.Empty(t, out.String())
}

func TestKstackRejectsExtraArguments(t *testing.T) {
	t.Parallel()
	never := func(context.Context) error { t.Error("ran"); return nil }
	out, status := execute(t, Deps{TUI: never}, "nope")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, `unknown command "nope"`)
}
