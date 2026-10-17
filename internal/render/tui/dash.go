package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/i18n"
)

// DashJob describes the control console for one account.
type DashJob struct {
	Account  string
	Products []string
	// Scan loads the account snapshot; it should reuse the cloud's cache.
	Scan func(ctx context.Context) (AccountView, error)
	// Checks runs the environment self-test (identity, permissions, role).
	Checks func(ctx context.Context) ([]core.Check, error)
}

// DashOutcome is the function the operator chose. The CLI performs it after the
// console has handed the terminal back, so the console can be reopened when the
// action finishes.
type DashOutcome struct {
	// Action is update, clean, status or "" (quit).
	Action string
}

// Menu entries, in display order.
const (
	dashUpdate = iota
	dashClean
	dashStatus
	dashChecks
)

var dashItemKeys = []struct{ label, badge string }{
	{"dash.item.update", "dash.badge.update"},
	{"dash.item.clean", "dash.badge.clean"},
	{"dash.item.status", ""},
	{"dash.item.checks", ""},
}

type dashModel struct {
	bundle *i18n.Bundle
	job    DashJob
	ctx    context.Context
	cancel context.CancelFunc

	cursor  int
	loading bool
	loaded  bool
	scanErr error
	view    AccountView

	checksLoading bool
	checksLoaded  bool
	checksErr     error
	checks        []core.Check
	showChecks    bool

	width   int
	height  int
	spinner spinner.Model
	styles  styles
	outcome DashOutcome
}

type dashScanMsg struct {
	view AccountView
	err  error
}

type dashChecksMsg struct {
	checks []core.Check
	err    error
}

func newDashModel(ctx context.Context, cancel context.CancelFunc, bundle *i18n.Bundle, job DashJob, noColor bool) *dashModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)
	return &dashModel{
		bundle: bundle, job: job, ctx: ctx, cancel: cancel,
		loading: true, width: 100, spinner: sp, styles: newStyles(noColor),
	}
}

// Init implements tea.Model.
func (m *dashModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.scanCmd(), m.checksCmd())
}

func (m *dashModel) scanCmd() tea.Cmd {
	job := m.job
	ctx := m.ctx
	return func() tea.Msg {
		view, err := job.Scan(ctx)
		return dashScanMsg{view: view, err: err}
	}
}

func (m *dashModel) checksCmd() tea.Cmd {
	job := m.job
	ctx := m.ctx
	return func() tea.Msg {
		checks, err := job.Checks(ctx)
		return dashChecksMsg{checks: checks, err: err}
	}
}

// Update implements tea.Model.
func (m *dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case dashScanMsg:
		m.loading = false
		m.loaded = msg.err == nil
		m.scanErr = msg.err
		m.view = msg.view
		return m, nil

	case dashChecksMsg:
		m.checksLoading = false
		m.checksLoaded = msg.err == nil
		m.checksErr = msg.err
		m.checks = msg.checks
		if msg.err != nil {
			m.showChecks = true
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *dashModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.cancel()
		return m, tea.Quit
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(dashItemKeys)) % len(dashItemKeys)
		return m, nil
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(dashItemKeys)
		return m, nil
	case "enter":
		return m.activate(m.cursor)
	case "d":
		return m.activate(dashChecks)
	case "r":
		m.loading = true
		m.scanErr = nil
		m.checksLoading, m.checksLoaded, m.checksErr = true, false, nil
		return m, tea.Batch(m.scanCmd(), m.checksCmd())
	}

	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
		idx := int(msg.Runes[0] - '1')
		if idx < len(dashItemKeys) {
			m.cursor = idx
			return m.activate(idx)
		}
	}
	return m, nil
}

// activate runs a menu entry. Actions that change the cloud or switch screens are
// handed back to the CLI; the self-test stays inside the console.
func (m *dashModel) activate(idx int) (tea.Model, tea.Cmd) {
	switch idx {
	case dashChecks:
		m.showChecks = !m.showChecks
		if m.showChecks && !m.checksLoaded && !m.checksLoading {
			m.checksLoading = true
			return m, m.checksCmd()
		}
		return m, nil
	case dashUpdate, dashClean, dashStatus:
		if m.loading {
			// Refuse to act on stale/absent data: wait for the scan.
			return m, nil
		}
		if idx != dashStatus && m.scanErr != nil {
			return m, nil
		}
		switch idx {
		case dashUpdate:
			m.outcome.Action = "update"
		case dashClean:
			m.outcome.Action = "clean"
		case dashStatus:
			m.outcome.Action = "status"
		}
		m.cancel()
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model.
func (m *dashModel) View() string {
	width := m.width
	if width < 40 {
		width = 100
	}

	head := m.styles.title.Render(m.bundle.T("dash.account_line",
		"account", m.job.Account, "products", strings.Join(m.job.Products, ", ")))
	if m.loading {
		head += "  " + m.spinner.View() + " " + m.bundle.T("dash.scanning")
	} else if !m.view.ScannedAt.IsZero() {
		head += "  " + m.styles.subtitle.Render(m.bundle.T("status.scanned_at", "at", m.view.ScannedAt.Format("15:04:05")))
	}
	header := panel(m.styles, "Orbit · "+m.bundle.T("dash.title"), head, width, false)

	blocks := []string{header, m.overviewPanel(width), m.menuPanel(width)}
	if m.showChecks {
		blocks = append(blocks, m.checksPanel(width))
	}
	return screen(width, m.height, m.footer(), blocks...)
}

func (m *dashModel) overviewPanel(width int) string {
	if m.loading {
		return panel(m.styles, m.bundle.T("dash.overview"), m.spinner.View()+" "+m.bundle.T("dash.scanning"), width, false)
	}
	if m.scanErr != nil {
		return panel(m.styles, m.bundle.T("dash.overview"),
			m.styles.fail.Render(m.bundle.T("dash.failed", "error", m.scanErr.Error())), width, true)
	}
	v := m.view
	line1 := m.bundle.T("status.summary",
		"total", v.Total, "uploaded", v.Uploaded, "inuse", v.InUse, "unused", v.Unused,
		"resources", v.Resources, "unbound", v.Unbound, "foreign", v.Foreign)
	line2 := m.bundle.T("dash.badges",
		"pending", len(v.Pending), "candidates", len(v.Candidates), "warnings", len(v.Warnings))
	body := line1 + "\n" + line2
	for _, w := range v.Warnings {
		body += "\n" + m.styles.warn.Render("! "+w)
	}
	return panel(m.styles, m.bundle.T("dash.overview"), body, width, false)
}

func (m *dashModel) menuPanel(width int) string {
	var b strings.Builder
	for i, item := range dashItemKeys {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}
		label := m.bundle.T(item.label)
		line := cursor + label
		if badge := m.badge(i); badge != "" {
			line += m.styles.dim.Render("  " + badge)
		}
		if i == m.cursor {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line)
		if i < len(dashItemKeys)-1 {
			b.WriteString("\n")
		}
	}
	return panel(m.styles, m.bundle.T("dash.menu_title"), b.String(), width, true)
}

func (m *dashModel) badge(idx int) string {
	if m.scanErr != nil || m.loading {
		// Unknown is not zero: do not claim "0 pending" when the scan failed.
		return ""
	}
	switch idx {
	case dashUpdate:
		return m.bundle.T("dash.badge.update", "count", len(m.view.Pending))
	case dashClean:
		return m.bundle.T("dash.badge.clean", "count", len(m.view.Candidates))
	}
	return ""
}

func (m *dashModel) checksPanel(width int) string {
	switch {
	case m.checksLoading:
		return panel(m.styles, m.bundle.T("dash.checks_title"),
			m.spinner.View()+" "+m.bundle.T("dash.checks_loading"), width, false)
	case m.checksErr != nil:
		return panel(m.styles, m.bundle.T("dash.checks_title"),
			m.styles.fail.Render(m.bundle.T("dash.checks_failed", "error", m.checksErr.Error())), width, false)
	case len(m.checks) == 0:
		return panel(m.styles, m.bundle.T("dash.checks_title"),
			m.styles.dim.Render(m.bundle.T("status.none")), width, false)
	}

	var b strings.Builder
	for i, check := range m.checks {
		mark, style := "✓", m.styles.ok
		switch check.Status {
		case core.CheckWarn:
			mark, style = "!", m.styles.warn
		case core.CheckFail:
			mark, style = "✗", m.styles.fail
		}
		label := m.bundle.T(check.Key)
		if check.MessageKey != "" {
			label = m.bundle.T(check.MessageKey)
		}
		line := style.Render(mark) + " " + fmt.Sprintf("%-26s", truncate(label, 26))
		if check.Detail != "" {
			line += m.styles.dim.Render(check.Detail)
		}
		b.WriteString(line)
		if i < len(m.checks)-1 {
			b.WriteString("\n")
		}
	}
	return panel(m.styles, m.bundle.T("dash.checks_title"), b.String(), width, false)
}

func (m *dashModel) footer() string {
	return keyHints(m.styles, m.width, [][2]string{
		{"↑/↓", m.bundle.T("keys.move")},
		{"enter", m.bundle.T("keys.run")},
		{"d", m.bundle.T("keys.checks")},
		{"r", m.bundle.T("keys.rescan")},
		{"q", m.bundle.T("keys.quit")},
	})
}

// RunDash shows the console until the operator picks a function or quits.
func RunDash(ctx context.Context, bundle *i18n.Bundle, job DashJob) (DashOutcome, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	noColor := os.Getenv("NO_COLOR") != ""
	model := newDashModel(runCtx, cancel, bundle, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	final, err := program.Run()
	if err != nil {
		return DashOutcome{}, err
	}
	if done, ok := final.(*dashModel); ok {
		return done.outcome, nil
	}
	return DashOutcome{}, nil
}
