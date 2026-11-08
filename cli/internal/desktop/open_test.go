package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSteps records each step it is asked for and fails the one named in fail.
type fakeSteps struct {
	installed string
	fail      string
	calls     []string
}

var errStep = errors.New("step failed")

func (f *fakeSteps) step(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errStep
	}
	return nil
}

func (f *fakeSteps) Locate() (string, error) {
	return f.installed, f.step("locate")
}

func (f *fakeSteps) Download(_ context.Context, progress func(done, total int64)) (string, error) {
	progress(5, 10)
	progress(10, 10)
	return "/tmp/installer", f.step("download")
}

func (f *fakeSteps) Install(_ context.Context, installer string, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Running the installer")
	return "/installed/Kstack", f.step("install " + installer)
}

func (f *fakeSteps) Launch(path string) error {
	return f.step("launch " + path)
}

func TestAnInstalledAppIsLaunched(t *testing.T) {
	t.Parallel()
	f := &fakeSteps{installed: "/Applications/Kstack.app"}
	var out bytes.Buffer
	require.NoError(t, Open(context.Background(), f, &out))
	assert.Equal(t, []string{"locate", "launch /Applications/Kstack.app"}, f.calls)
	assert.Equal(t, "Opening /Applications/Kstack.app\n", out.String())
}

func TestAMissingAppIsDownloadedInstalledAndLaunched(t *testing.T) {
	t.Parallel()
	f := &fakeSteps{}
	var out bytes.Buffer
	require.NoError(t, Open(context.Background(), f, &out))
	assert.Equal(t, []string{"locate", "download", "install /tmp/installer", "launch /installed/Kstack"}, f.calls)
	assert.Equal(t, "Kstack is not installed. Downloading it...\n"+
		"\r 50% of 10 B\x1b[K\r100% of 10 B\x1b[K\n"+
		"Installing...\n"+
		"Running the installer\n"+
		"Opening /installed/Kstack\n", out.String())
}

func TestAFailedStepStopsTheRest(t *testing.T) {
	t.Parallel()
	for fail, want := range map[string][]string{
		"locate":                   {"locate"},
		"download":                 {"locate", "download"},
		"install /tmp/installer":   {"locate", "download", "install /tmp/installer"},
		"launch /installed/Kstack": {"locate", "download", "install /tmp/installer", "launch /installed/Kstack"},
	} {
		f := &fakeSteps{fail: fail}
		require.ErrorIs(t, Open(context.Background(), f, &bytes.Buffer{}), errStep, fail)
		assert.Equal(t, want, f.calls, fail)
	}
}
