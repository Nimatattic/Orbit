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
	// Keys and Certs carry the selection. Keys are item keys ("product|domain")
	// for update; Certs are certificate ids for cleanup. Empty means "everything".
	Keys  []string
	Certs []string
}

// Sections the cursor can move between.
const (
	sectionPending = iota
	sectionCandidates
	sectionCount
)

type accountState struct {
	// attempted separates "we already tried" from "it worked": without it a
	// failed account would be re-queued forever.
	attempted bool
	loading   bool
	loaded    bool
	err       error
	view      AccountView
	cursor    [sectionCount]int
	selected  [sectionCount]map[string]bool
}

func (s *accountState) items(section int) []Item {
	if section == sectionPending {
		return s.view.Pending
	}
	return s.view.Candidates
}

func (s *accountState) marks(section int) map[string]bool {
	if s.selected[section] == nil {
		s.selected[section] = map[string]bool{}
	}
	return s.selected[section]
}

func (s *accountState) selectedItems(section int) []Item {
	marks := s.marks(section)
	out := make([]Item, 0, len(marks))
	for _, it := range s.items(section) {
		if marks[it.Key] {
			out = append(out, it)
		}
	}
	return out
}

type statusModel struct {
	bundle  *i18n.Bundle
	job     StatusJob
	ctx     context.Context
	cancel  context.CancelFunc
	states  map[string]*accountState
	order   []string
	tab     int
	focus   int
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
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)
	states := make(map[string]*accountState, len(job.Accounts))
	for _, name := range job.Accounts {
		states[name] = &accountState{}
	}
	return &statusModel{
		bundle: bundle, job: job, ctx: ctx, cancel: cancel,
		states: states, order: append([]string(nil), job.Accounts...),
		width: 100, spinner: sp, styles: newStyles(noColor),
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

func (m *statusModel) currentState() *accountState {
	if len(m.order) == 0 {
		return nil
	}
	return m.states[m.order[m.tab]]
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
			st.cursor = [sectionCount]int{}
			st.selected = [sectionCount]map[string]bool{}
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
		m.tab = (m.tab + 1) % maxInt(1, len(m.order))
		return m, nil
	case "shift+tab", "left", "h":
		m.tab = (m.tab - 1 + maxInt(1, len(m.order))) % maxInt(1, len(m.order))
		return m, nil

	case "s":
		// Switch the active list so the cursor can work on either section.
		m.focus = (m.focus + 1) % sectionCount
		return m, nil

	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil

	case " ":
		m.toggleCursor()
		return m, nil
	case "a":
		m.setAll(true)
		return m, nil
	case "n":
		m.setAll(false)
		return m, nil

	case "r":
		if st := m.currentState(); st != nil {
			st.attempted, st.loading, st.loaded, st.err = true, true, false, nil
			return m, m.scanCmd(m.order[m.tab])
		}
		return m, nil

	case "u", "enter":
		st := m.currentState()
		if st == nil {
			return m, nil
		}
		m.outcome = StatusOutcome{Action: "update", Account: m.order[m.tab]}
		for _, it := range st.selectedItems(sectionPending) {
			m.outcome.Keys = append(m.outcome.Keys, it.Key)
		}
		m.cancel()
		return m, tea.Quit

	case "c":
		st := m.currentState()
		if st == nil {
			return m, nil
		}
		m.outcome = StatusOutcome{Action: "clean", Account: m.order[m.tab]}
		for _, it := range st.selectedItems(sectionCandidates) {
			m.outcome.Certs = append(m.outcome.Certs, it.Note)
		}
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

func (m *statusModel) moveCursor(delta int) {
	st := m.currentState()
	if st == nil {
		return
	}
	n := len(st.items(m.focus))
	if n == 0 {
		st.cursor[m.focus] = 0
		return
	}
	next := st.cursor[m.focus] + delta
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	st.cursor[m.focus] = next
}

func (m *statusModel) toggleCursor() {
	st := m.currentState()
	if st == nil {
		return
	}
	items := st.items(m.focus)
	if len(items) == 0 {
		return
	}
	idx := st.cursor[m.focus]
	if idx >= len(items) {
		idx = len(items) - 1
		st.cursor[m.focus] = idx
	}
	key := items[idx].Key
	marks := st.marks(m.focus)
	marks[key] = !marks[key]
}

func (m *statusModel) setAll(value bool) {
	st := m.currentState()
	if st == nil {
		return
	}
	marks := st.marks(m.focus)
	for _, it := range st.items(m.focus) {
		marks[it.Key] = value
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *statusModel) current() string {
	if len(m.order) == 0 {
		return ""
	}
	return m.order[m.tab]
}

// View implements tea.Model.
func (m *statusModel) View() string {
	width := m.windowWidth()

	header := panel(m.styles, "Orbit · "+m.job.Title,
		m.styles.subtitle.Render(m.bundle.T("status.subtitle")), width, false)
	tabs := panel(m.styles, m.bundle.T("status.accounts"), m.renderTabs(), width, false)

	if len(m.order) == 0 {
		body := panel(m.styles, m.bundle.T("status.title"),
			m.styles.warn.Render(m.bundle.T("status.no_accounts")), width, true)
		return screen(width, m.height,
			keyHints(m.styles, width, [][2]string{{"q", m.bundle.T("keys.quit")}}), header, tabs, body)
	}

	st := m.currentState()
	account := panel(m.styles, m.current(), truncateLines(m.renderAccount(st), width-2), width, false)

	if st.loading && !st.loaded {
		body := panel(m.styles, m.bundle.T("status.title"),
			m.spinner.View()+" "+m.bundle.T("status.loading", "account", m.current()), width, true)
		return screen(width, m.height, m.footer(), header, tabs, account, body)
	}
	if st.err != nil {
		body := panel(m.styles, m.bundle.T("status.title"),
			m.styles.fail.Render(m.bundle.T("status.failed", "error", st.err.Error())), width, true)
		return screen(width, m.height, m.footer(), header, tabs, account, body)
	}

	pending := panel(m.styles,
		m.bundle.T("status.pending", "count", len(st.view.Pending))+"  "+m.selectionLabel(st, sectionPending),
		m.renderItems(st, sectionPending), width, m.focus == sectionPending)
	candidates := panel(m.styles,
		m.bundle.T("status.candidates", "count", len(st.view.Candidates))+"  "+m.selectionLabel(st, sectionCandidates),
		m.renderItems(st, sectionCandidates), width, m.focus == sectionCandidates)

	return screen(width, m.height, m.footer(), header, tabs, account, pending, candidates)
}

// windowWidth keeps a usable width even before the first WindowSizeMsg.
func (m *statusModel) windowWidth() int {
	if m.width < 40 {
		return 100
	}
	return m.width
}

func (m *statusModel) footer() string {
	// Ordered by importance: the adaptor drops from the end on narrow screens.
	return keyHints(m.styles, m.windowWidth(), [][2]string{
		{"↑/↓", m.bundle.T("keys.move")},
		{"space", m.bundle.T("keys.toggle")},
		{"a", m.bundle.T("keys.select_all")},
		{"n", m.bundle.T("keys.select_none")},
		{"u", m.bundle.T("keys.apply")},
		{"c", m.bundle.T("keys.clean")},
		{"s", m.bundle.T("keys.section")},
		{"q", m.bundle.T("keys.quit")},
		{"tab", m.bundle.T("keys.account")},
		{"r", m.bundle.T("keys.rescan")},
	})
}

func (m *statusModel) selectionLabel(st *accountState, section int) string {
	n := len(st.marks(section))
	if n == 0 {
		return ""
	}
	return m.styles.active.Render(m.bundle.T("status.selected", "count", n))
}

// renderTabs draws one tab per account with a state marker.
func (m *statusModel) renderTabs() string {
	if len(m.order) == 0 {
		return m.styles.dim.Render(m.bundle.T("status.none"))
	}
	parts := make([]string, 0, len(m.order))
	for i, name := range m.order {
		st := m.states[name]
		label := fmt.Sprintf("%d %s", i+1, name)
		switch {
		case st.err != nil:
			label += " !"
		case st.loading && !st.loaded:
			label += " " + m.spinner.View()
		case st.loaded && len(st.view.Pending) > 0:
			label += fmt.Sprintf(" (%d)", len(st.view.Pending))
		}
		if i == m.tab {
			parts = append(parts, m.styles.active.Render("▌"+label+"▐"))
			continue
		}
		parts = append(parts, m.styles.dim.Render(" "+label+" "))
	}
	return strings.Join(parts, " ")
}

func (m *statusModel) renderAccount(st *accountState) string {
	if st.loading && !st.loaded {
		return m.spinner.View() + " " + m.bundle.T("status.loading", "account", m.current())
	}
	if st.err != nil {
		return m.styles.fail.Render(m.bundle.T("status.failed", "error", st.err.Error()))
	}
	if !st.loaded {
		return m.styles.dim.Render(m.bundle.T("status.pending_scan"))
	}

	v := st.view
	var b strings.Builder
	b.WriteString(m.bundle.T("status.summary",
		"total", v.Total, "uploaded", v.Uploaded, "inuse", v.InUse, "unused", v.Unused,
		"resources", v.Resources, "unbound", v.Unbound, "foreign", v.Foreign))
	if len(v.Products) > 0 {
		b.WriteString("  " + m.styles.dim.Render(m.bundle.T("tui.summary.products", "products", strings.Join(v.Products, ", "))))
	}
	if !v.ScannedAt.IsZero() {
		b.WriteString("  " + m.styles.dim.Render(m.bundle.T("status.scanned_at", "at", v.ScannedAt.Format("15:04:05"))))
	}
	for _, w := range v.Warnings {
		b.WriteString("\n" + m.styles.warn.Render("! "+w))
	}
	return b.String()
}

// renderItems draws one section with a cursor and checkboxes.
func (m *statusModel) renderItems(st *accountState, section int) string {
	items := st.items(section)
	if len(items) == 0 {
		return m.styles.dim.Render("  " + m.bundle.T("status.none"))
	}
	limit := m.listRows()
	cursor := st.cursor[section]
	start := 0
	if cursor >= limit {
		start = cursor - limit + 1
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	marks := st.marks(section)
	var b strings.Builder
	for i := start; i < end; i++ {
		it := items[i]
		focused := section == m.focus
		marker := "  "
		if focused && i == cursor {
			marker = "▸ "
		}
		box := m.styles.dim.Render("[ ]")
		if marks[it.Key] {
			box = m.styles.ok.Render("[x]")
		}
		target := it.Target
		if target == "" {
			target = it.Note
		}
		line := fmt.Sprintf("%s%s %-6s %-40s %s", marker, box,
			strings.ToUpper(it.Product), truncate(it.Domain, 40), truncate(target, 16))
		if focused && i == cursor {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(items) {
		b.WriteString("\n" + m.styles.dim.Render(fmt.Sprintf("  ... +%d", len(items)-end)))
	}
	return b.String()
}

// listRows splits the remaining height between the two sections.
func (m *statusModel) listRows() int {
	if m.height <= 0 {
		return 6
	}
	// Four bordered panels (header, tabs, account, and one list) plus the
	// second list and the footer: leave room for all of them.
	rows := (m.height - 20) / 2
	if rows < 2 {
		rows = 2
	}
	if rows > 12 {
		rows = 12
	}
	return rows
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
