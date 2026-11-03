// Package tui is kstack's terminal UI: for now a placeholder that signs the
// user up for the newsletter.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const title = ` _  __     _             _
| |/ /___| |_ __ _  ___| | __
| ' // __| __/ _` + "`" + ` |/ __| |/ /
| . \\__ \ || (_| | (__|   <
|_|\_\___/\__\__,_|\___|_|\_\`

type state int

const (
	editing state = iota
	submitting
	subscribed
)

// subscribedMsg carries a submit's result back into Update.
type subscribedMsg struct{ err error }

// Model is the placeholder TUI. subscribe runs off the update loop, as a
// tea.Cmd, so a slow server never freezes the screen.
type Model struct {
	input     textinput.Model
	subscribe func(email string) error
	state     state
	errText   string
}

// New returns the model with the email box focused.
func New(subscribe func(email string) error) Model {
	input := textinput.New()
	input.Placeholder = "you@example.com"
	input.Prompt = ""
	input.Width = 36
	input.Focus()
	return Model{input: input, subscribe: subscribe}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles a key or a submit's result. Esc and Ctrl+C always quit; q
// quits once the box is gone, since before that it may be part of an address.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC:
			return m, tea.Quit
		case m.state == subscribed:
			if msg.String() == "q" {
				return m, tea.Quit
			}
			return m, nil
		case m.state == submitting:
			return m, nil
		case msg.Type == tea.KeyEnter:
			return m.submit()
		}
	case subscribedMsg:
		if msg.err != nil {
			m.state = editing
			m.errText = "Couldn't subscribe: " + msg.err.Error()
			return m, nil
		}
		m.state = subscribed
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	email := strings.TrimSpace(m.input.Value())
	if !validEmail(email) {
		m.errText = "That doesn't look like an email address."
		return m, nil
	}
	m.state = submitting
	m.errText = ""
	return m, func() tea.Msg { return subscribedMsg{err: m.subscribe(email)} }
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	helpStyle  = lipgloss.NewStyle().Faint(true)
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString("TUI coming soon...\n\n")
	if m.state == subscribed {
		email := strings.TrimSpace(m.input.Value())
		b.WriteString(okStyle.Render("Thanks! "+email+" is subscribed.") + "\n\n")
		b.WriteString(helpStyle.Render("q or esc quit") + "\n")
		return b.String()
	}
	b.WriteString("Get the newsletter:\n" + boxStyle.Render(m.input.View()) + "\n")
	if m.state == submitting {
		b.WriteString("Subscribing...\n")
	} else if m.errText != "" {
		b.WriteString(errStyle.Render(m.errText) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("enter subscribe • esc quit") + "\n")
	return b.String()
}
