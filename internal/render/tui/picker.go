package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Crequency/orbit/internal/i18n"
)

// PickerJob describes the account multi-select screen.
type PickerJob struct {
	Title    string
	Accounts []string
	// Detail optionally annotates each account (credential source, regions, ...).
	Detail map[string]string
}

type pickerModel struct {
	bundle *i18n.Bundle
	job    PickerJob
	cursor int
	sel    map[string]bool
	styles styles
	width  int
	height int
	done   bool
	cancel bool
	errKey string
}

func newPickerModel(bundle *i18n.Bundle, job PickerJob, noColor bool) *pickerModel {
	return &pickerModel{
		bundle: bundle,
		job:    job,
		sel:    map[string]bool{},
		styles: newStyles(noColor),
	}
}

// Init implements tea.Model.
func (m *pickerModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancel = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.job.Accounts)-1 {
				m.cursor++
			}
			return m, nil
		case " ":
			if len(m.job.Accounts) > 0 {
				name := m.job.Accounts[m.cursor]
				m.sel[name] = !m.sel[name]
				m.errKey = ""
			}
			return m, nil
		case "a", "A":
			all := len(m.selected()) != len(m.job.Accounts)
			for _, name := range m.job.Accounts {
				m.sel[name] = all
			}
			m.errKey = ""
			return m, nil
		case "enter":
			if len(m.selected()) == 0 {
				// Refuse rather than silently doing nothing.
				m.errKey = "picker.none_selected"
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m *pickerModel) View() string {
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Orbit · " + m.job.Title))
	b.WriteString("\n\n")

	for i, name := range m.job.Accounts {
		cursor := " "
		if i == m.cursor {
			cursor = m.styles.active.Render(">")
		}
		box := "[ ]"
		if m.sel[name] {
			box = m.styles.ok.Render("[x]")
		}
		line := fmt.Sprintf("%s %s %s", cursor, box, name)
		if detail := m.job.Detail[name]; detail != "" {
			line += m.styles.dim.Render("  " + detail)
		}
		if i == m.cursor {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(m.bundle.T("picker.selected", "count", len(m.selected()), "total", len(m.job.Accounts)))
	b.WriteString("\n")
	if m.errKey != "" {
		b.WriteString(m.styles.fail.Render(m.bundle.T(m.errKey)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.styles.dim.Render(m.bundle.T("picker.help")))
	return fitWidth(b.String(), m.width)
}

func (m *pickerModel) selected() []string {
	out := make([]string, 0, len(m.job.Accounts))
	for _, name := range m.job.Accounts {
		if m.sel[name] {
			out = append(out, name)
		}
	}
	return out
}

// PickAccounts shows the multi-select screen and returns the chosen accounts.
// confirmed is false when the operator cancelled.
func PickAccounts(ctx context.Context, bundle *i18n.Bundle, job PickerJob) (selected []string, confirmed bool, err error) {
	if len(job.Accounts) <= 1 {
		return job.Accounts, true, nil
	}
	noColor := os.Getenv("NO_COLOR") != ""
	model := newPickerModel(bundle, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	final, err := program.Run()
	if err != nil {
		return nil, false, err
	}
	done, ok := final.(*pickerModel)
	if !ok || done.cancel || !done.done {
		return nil, false, nil
	}
	return done.selected(), true, nil
}
