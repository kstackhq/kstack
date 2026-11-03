package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake records the addresses the model submits and answers with err.
type fake struct {
	got []string
	err error
}

func (f *fake) subscribe(email string) error {
	f.got = append(f.got, email)
	return f.err
}

func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func typed(t *testing.T, m Model, s string) Model {
	t.Helper()
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return m
}

func enter(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	return send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

// isQuit reports whether cmd quits the program. Never pass it the text
// input's command: that is the cursor's blink, which waits on a real timer.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestAnInvalidAddressShowsAnErrorWithoutSubmitting(t *testing.T) {
	t.Parallel()
	f := &fake{}
	m, cmd := enter(t, typed(t, New(f.subscribe), "ada@example"))
	assert.Nil(t, cmd)
	assert.Empty(t, f.got)
	assert.Contains(t, m.View(), "That doesn't look like an email address.")
}

func TestAValidAddressIsSubmittedAndConfirmed(t *testing.T) {
	t.Parallel()
	f := &fake{}
	m := typed(t, New(f.subscribe), "  ada@example.com ")
	m, cmd := enter(t, m)
	require.NotNil(t, cmd)
	assert.Contains(t, m.View(), "Subscribing...")

	// A second Enter while the first is in flight submits nothing more.
	m, again := enter(t, m)
	assert.Nil(t, again)

	m, _ = send(t, m, cmd())
	assert.Equal(t, []string{"ada@example.com"}, f.got)
	assert.Contains(t, m.View(), "Thanks! ada@example.com is subscribed.")
	assert.NotContains(t, m.View(), "Subscribing...")
}

func TestAFailedSubmitShowsTheErrorAndLetsTheUserRetry(t *testing.T) {
	t.Parallel()
	f := &fake{err: errors.New("the server answered 502")}
	m := typed(t, New(f.subscribe), "ada@example.com")
	m, cmd := enter(t, m)
	m, _ = send(t, m, cmd())
	assert.Contains(t, m.View(), "Couldn't subscribe: the server answered 502")

	f.err = nil
	m, cmd = enter(t, m)
	require.NotNil(t, cmd)
	m, _ = send(t, m, cmd())
	assert.Equal(t, []string{"ada@example.com", "ada@example.com"}, f.got)
	assert.Contains(t, m.View(), "is subscribed.")
	assert.NotContains(t, m.View(), "Couldn't subscribe")
}

func TestEscAndCtrlCQuit(t *testing.T) {
	t.Parallel()
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		_, cmd := send(t, typed(t, New((&fake{}).subscribe), "ada"), tea.KeyMsg{Type: k})
		assert.True(t, isQuit(cmd), k)
	}
}

func TestQIsTypedIntoTheBoxAndQuitsOnceSubscribed(t *testing.T) {
	t.Parallel()
	f := &fake{}
	m := typed(t, New(f.subscribe), "q")
	assert.Equal(t, "q", m.input.Value())
	m = typed(t, m, "@example.com")
	m, cmd := enter(t, m)
	m, _ = send(t, m, cmd())
	assert.Equal(t, []string{"q@example.com"}, f.got)

	_, cmd = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	assert.True(t, isQuit(cmd))
}

func TestKeysOtherThanQuitAreIgnoredOnceSubscribed(t *testing.T) {
	t.Parallel()
	f := &fake{}
	m, cmd := enter(t, typed(t, New(f.subscribe), "ada@example.com"))
	m, _ = send(t, m, cmd())
	before := m.View()
	m, cmd = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	assert.Nil(t, cmd)
	assert.Equal(t, before, m.View())
}

func TestTheViewShowsTheTitleThePlaceholderLineAndTheBox(t *testing.T) {
	t.Parallel()
	v := New((&fake{}).subscribe).View()
	assert.Contains(t, v, `| |/ /___| |_ __ _  ___| | __`)
	assert.Contains(t, v, "TUI coming soon...")
	assert.Contains(t, v, "╭")
	assert.Contains(t, v, "you@example.com")
	assert.Contains(t, v, "esc quit")
}

func TestInitBlinksTheCursor(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, New((&fake{}).subscribe).Init())
}
