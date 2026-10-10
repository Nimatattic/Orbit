package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Crequency/orbit/internal/i18n"
)

// AccountView is the per-account snapshot the status screen renders.
type AccountView struct {
	Account    string
	Products   []string
	Warnings   []string
	Total      int
	Uploaded   int
	InUse      int
	Unused     int
	Resources  int
	Unbound    int
	Foreign    int
	Pending    []Item
	Candidates []Item
	ScannedAt  time.Time
}

// StatusJob describes the multi-tab overview.
type StatusJob struct {
	Title    string
	Accounts []string
	// Scan collects one account. Implementations should reuse the cloud's
	// association cache: this screen is for looking, not for acting.
	Scan func(ctx context.Context, account string) (AccountView, error)
}

// StatusOutcome reports what the operator asked for beyond looking.
type StatusOutcome struct {
	// Action is "", "update" or "clean" when the operator pressed u/c.
	Action  string
	Account string
}

type accountState struct {
	// attempted separates "we already tried" from "it worked": without it a
	// failed account would be re-queued forever.
	attempted bool
	loading   bool
	loaded    bool
	err       error
	view      AccountView
}

type statusModel struct {
	bundle  *i18n.Bundle
	job     StatusJob
	ctx     context.Context
	cancel  context.CancelFunc
	states  map[string]*accountState
	order   []string
	tab     int
	width   int
	height  int
	spinner spinner.Model
	styles  styles
	outcome StatusOutcome
}

type accountScannedMsg struct {
	account string
	view    AccountView
	err     error
}

func newStatusModel(ctx context.Context, cancel context.CancelFunc, bundle *i18n.Bundle, job StatusJob, noColor bool) *statusModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	states := make(map[string]*accountState, len(job.Accounts))
	for _, name := range job.Accounts {
		states[name] = &accountState{}
	}
	return &statusModel{
		bundle: bundle, job: job, ctx: ctx, cancel: cancel,
		states: states, order: append([]string(nil), job.Accounts...),
		spinner: sp, styles: newStyles(noColor),
	}
}

// Init implements tea.Model.
func (m *statusModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.scheduleNext())
}

// scheduleNext scans the next account that has not been loaded yet. Accounts are
// scanned one at a time so the first tab becomes usable immediately instead of
// waiting for every account.
func (m *statusModel) scheduleNext() tea.Cmd {
	for _, name := range m.order {
		st := m.states[name]
		if !st.attempted && !st.loading {
			st.attempted = true
			st.loading = true
			return m.scanCmd(name)
		}
	}
	return nil
}

func (m *statusModel) scanCmd(account string) tea.Cmd {
	job := m.job
	ctx := m.ctx
	return func() tea.Msg {
		view, err := job.Scan(ctx, account)
		return accountScannedMsg{account: account, view: view, err: err}
	}
}

// Update implements tea.Model.
func (m *statusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case accountScannedMsg:
		st := m.states[msg.account]
		if st != nil {
			// Marking it here (not only in scheduleNext) keeps the model correct
			// even when a result arrives without having been scheduled first.
			st.attempted = true
			st.loading = false
			st.loaded = msg.err == nil
			st.err = msg.err
			st.view = msg.view
		}
		return m, m.scheduleNext()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *statusModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.cancel()
		return m, tea.Quit

	case "tab", "right", "l":
		m.tab = (m.tab + 1) % len(m.order)
		return m, nil
	case "shift+tab", "left", "h":
		m.tab = (m.tab - 1 + len(m.order)) % len(m.order)
		return m, nil

	case "r":
		if len(m.order) == 0 {
			return m, nil
		}
		name := m.order[m.tab]
		st := m.states[name]
		// A manual refresh clears the previous outcome and retries.
		st.attempted, st.loading, st.loaded, st.err = true, true, false, nil
		return m, m.scanCmd(name)

	case "u":
		m.outcome = StatusOutcome{Action: "update", Account: m.current()}
		m.cancel()
		return m, tea.Quit
	case "c":
		m.outcome = StatusOutcome{Action: "clean", Account: m.current()}
		m.cancel()
		return m, tea.Quit
	}

	// Number keys jump straight to a tab.
	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
		idx := int(msg.Runes[0] - '1')
		if idx < len(m.order) {
			m.tab = idx
		}
	}
	return m, nil
}

func (m *statusModel) current() string {
	if len(m.order) == 0 {
		return ""
	}
	return m.order[m.tab]
}

// View implements tea.Model.
func (m *statusModel) View() string {
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Orbit · " + m.job.Title))
	b.WriteString("\n")
	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")

	if len(m.order) == 0 {
		b.WriteString(m.styles.warn.Render(m.bundle.T("status.no_accounts")))
		b.WriteString("\n\n")
		b.WriteString(m.styles.dim.Render(m.bundle.T("status.help_no_action")))
		return fitWidth(b.String(), m.width)
	}

	st := m.states[m.current()]
	switch {
	case st.loading && !st.loaded:
		b.WriteString(m.spinner.View() + " " + m.bundle.T("status.loading", "account", m.current()))
	case st.err != nil:
		b.WriteString(m.styles.fail.Render(m.bundle.T("status.failed", "error", st.err.Error())))
	case st.loaded:
		b.WriteString(m.renderAccount(st.view))
	default:
		b.WriteString(m.styles.dim.Render(m.bundle.T("status.pending_scan")))
	}

	b.WriteString("\n")
	b.WriteString(m.styles.dim.Render(m.bundle.T("status.help")))
	return fitWidth(b.String(), m.width)
}

// renderTabs draws one tab per account with a state marker.
func (m *statusModel) renderTabs() string {
	if len(m.order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.order))
	for i, name := range m.order {
		st := m.states[name]
		label := fmt.Sprintf("%d %s", i+1, name)
		switch {
		case st.err != nil:
			label += " !"
		case st.loading && !st.loaded:
			label += " …"
		case st.loaded && len(st.view.Pending) > 0:
			label += fmt.Sprintf(" (%d)", len(st.view.Pending))
		}
		if i == m.tab {
			parts = append(parts, m.styles.active.Render("["+label+"]"))
			continue
		}
		parts = append(parts, m.styles.dim.Render(" "+label+" "))
	}
	return " " + strings.Join(parts, " ")
}

func (m *statusModel) renderAccount(v AccountView) string {
	var b strings.Builder

	b.WriteString("  " + m.bundle.T("status.summary",
		"total", v.Total, "uploaded", v.Uploaded, "inuse", v.InUse, "unused", v.Unused,
		"resources", v.Resources, "unbound", v.Unbound, "foreign", v.Foreign))
	b.WriteString("\n")
	if len(v.Products) > 0 {
		b.WriteString(m.styles.dim.Render("  " + m.bundle.T("tui.summary.products", "products", strings.Join(v.Products, ", "))))
		b.WriteString("\n")
	}
	if !v.ScannedAt.IsZero() {
		b.WriteString(m.styles.dim.Render("  " + m.bundle.T("status.scanned_at", "at", v.ScannedAt.Format("15:04:05"))))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("  " + m.styles.title.Render(m.bundle.T("status.pending", "count", len(v.Pending))) + "\n")
	if len(v.Pending) == 0 {
		b.WriteString(m.styles.dim.Render("    "+m.bundle.T("status.none")) + "\n")
	} else {
		for _, it := range m.limit(v.Pending) {
			b.WriteString(fmt.Sprintf("    %-6s %-42s %s\n", strings.ToUpper(it.Product), truncate(it.Domain, 42), it.Target))
		}
	}
	b.WriteString("\n")

	b.WriteString("  " + m.styles.title.Render(m.bundle.T("status.candidates", "count", len(v.Candidates))) + "\n")
	if len(v.Candidates) == 0 {
		b.WriteString(m.styles.dim.Render("    "+m.bundle.T("status.none")) + "\n")
	} else {
		for _, it := range m.limit(v.Candidates) {
			b.WriteString(fmt.Sprintf("    %-42s %s\n", truncate(it.Domain, 42), it.Target))
		}
	}

	if len(v.Warnings) > 0 {
		b.WriteString("\n")
		b.WriteString("  " + m.styles.warn.Render(m.bundle.T("status.warnings", "count", len(v.Warnings))) + "\n")
		for _, w := range v.Warnings {
			b.WriteString(m.styles.warn.Render("    "+w) + "\n")
		}
	}
	return b.String()
}

// limit caps a section so the frame fits the terminal.
func (m *statusModel) limit(items []Item) []Item {
	max := 5
	if m.height > 0 {
		max = (m.height - 14) / 2
	}
	if max < 2 {
		max = 2
	}
	if len(items) <= max {
		return items
	}
	out := make([]Item, 0, max+1)
	out = append(out, items[:max]...)
	out = append(out, Item{Domain: fmt.Sprintf("... +%d", len(items)-max)})
	return out
}

// RunStatus shows the multi-tab overview until the operator quits.
func RunStatus(ctx context.Context, bundle *i18n.Bundle, job StatusJob) (StatusOutcome, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	noColor := os.Getenv("NO_COLOR") != ""
	model := newStatusModel(runCtx, cancel, bundle, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	final, err := program.Run()
	if err != nil {
		return StatusOutcome{}, err
	}
	if done, ok := final.(*statusModel); ok {
		return done.outcome, nil
	}
	return StatusOutcome{}, nil
}
