package cmd

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppHelpDescribesTheInstall(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{}, "app", "--help")
	require.Zero(t, status)
	assert.Contains(t, out, "downloads this platform's file from the newest")
	assert.Contains(t, out, "kstack app [flags]")
}

func TestAppOpensTheAppAndReportsToTheCommandsOutput(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{App: func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "opened")
		return err
	}}, "app")
	require.Zero(t, status)
	assert.Equal(t, "opened", out)
}

func TestAppRejectsExtraArguments(t *testing.T) {
	t.Parallel()
	out, status := execute(t, Deps{App: func(context.Context, io.Writer) error { t.Error("ran"); return nil }}, "app", "nope")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, `unknown command "nope"`)
}
