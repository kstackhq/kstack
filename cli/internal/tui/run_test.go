package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDrawsTheScreenAndReturnsOnCtrlC(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("\x03"), &out, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	assert.Contains(t, out.String(), "TUI coming soon...")
}

func TestRunSubmitsWithItsContext(t *testing.T) {
	t.Parallel()
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "run's")
	var got []string
	in := strings.NewReader("ada@example.com\r")
	var out bytes.Buffer
	// Input ends after Enter; the subscribe answer quits by cancelling, so the
	// run cannot outlive the submit.
	ctx, cancel := context.WithCancel(ctx)
	err := Run(ctx, in, &out, func(c context.Context, email string) error {
		got = append(got, c.Value(key{}).(string)+" "+email)
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"run's ada@example.com"}, got)
}
