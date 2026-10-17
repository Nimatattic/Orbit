// Package tui renders an interactive, full-screen run view.
//
// It is used when Orbit is attached to a terminal and the operator neither asked
// for a dry run nor for batch mode: the plan is shown, the operator confirms, and
// then every change is displayed with live per-item state (including an animated
// indicator while a write is waiting for the cloud to report it as effective).
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/i18n"
)

// Item is one row of the run: a change that will be written, or a deletion.
type Item struct {
	Key     string // stable identity, e.g. "cdn|www.example.com"
	Product string
	Domain  string
	Target  string
	Note    string
}

// Job describes one interactive run.
type Job struct {
	// Title is a short banner, e.g. "update --ssl".
	Title string
	// Summary lines shown above the table (counts, account, ...).
	Summary []string
	// Scan produces the rows and is executed inside the TUI so that its progress
	// is visible too.
	Scan func(ctx context.Context) ([]Item, error)
	// Apply executes the confirmed rows.
	Apply func(ctx context.Context) ([]core.Result, error)
	// NoWrites is set when the scan found nothing to write; the TUI then only
	// reports the result.
	NoWrites bool
}

// Outcome reports what the interactive run did.
type Outcome struct {
	Confirmed bool
	Cancelled bool
	Results   []core.Result
	Scanned   []Item
}

type phase int

const (
	phaseScanning phase = iota
	phaseConfirm
	phaseRunning
	phaseDone
)

type itemState int

const (
	statePending itemState = iota
	stateSubmitting
	stateWaiting
	stateVerified
	stateUnverified
	stateFailed
)

type row struct {
	item   Item
	state  itemState
	detail string // extra detail: cloud state, error code or hint
	since  time.Time
}

type scanDoneMsg struct {
	items []Item
	err   error
}

type applyDoneMsg struct {
	results []core.Result
	err     error
}

type progressMsg struct{ event cloud.Progress }

type eventsClosedMsg struct{}

type tickMsg time.Time

// Model is the Bubble Tea model for one run.
type Model struct {
	bundle *i18n.Bundle
	job    Job
	events <-chan cloud.Progress
	ctx    context.Context
	cancel context.CancelFunc

	phase      phase
	rows       []*row
	byKey      map[string]*row
	scanLines  []string
	scanErr    error
	results    []core.Result
	applyErr   error
	cancelled  bool
	confirmed  bool
	started    time.Time
	finished   time.Time
	width      int
	height     int
	spinner    spinner.Model
	styles     styles
	eventsSeen int
}

func newModel(ctx context.Context, cancel context.CancelFunc, bundle *i18n.Bundle, events <-chan cloud.Progress, job Job, noColor bool) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	return &Model{
		bundle:  bundle,
		job:     job,
		events:  events,
		ctx:     ctx,
		cancel:  cancel,
		phase:   phaseScanning,
		byKey:   map[string]*row{},
		started: time.Now(),
		spinner: sp,
		styles:  newStyles(noColor),
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.pumpEvents(), m.runScan(), tickEvery(time.Second))
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// pumpEvents forwards provider progress events into the UI.
func (m *Model) pumpEvents() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return eventsClosedMsg{}
		}
		return progressMsg{event: ev}
	}
}

func (m *Model) runScan() tea.Cmd {
	job := m.job
	ctx := m.ctx
	return func() tea.Msg {
		items, err := job.Scan(ctx)
		return scanDoneMsg{items: items, err: err}
	}
}

func (m *Model) runApply() tea.Cmd {
	job := m.job
	ctx := m.ctx
	return func() tea.Msg {
		results, err := job.Apply(ctx)
		return applyDoneMsg{results: results, err: err}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.phase == phaseDone {
			return m, nil
		}
		return m, cmd

	case tickMsg:
		if m.phase == phaseDone {
			return m, nil
		}
		return m, tickEvery(time.Second)

	case eventsClosedMsg:
		return m, nil

	case progressMsg:
		m.applyProgress(msg.event)
		return m, m.pumpEvents()

	case scanDoneMsg:
		if msg.err != nil {
			m.scanErr = msg.err
			m.phase = phaseDone
			m.finished = time.Now()
			return m, nil
		}
		m.setItems(msg.items)
		switch {
		case m.job.NoWrites || len(m.pendingKeys()) == 0:
			m.phase = phaseDone
			m.finished = time.Now()
		default:
			m.phase = phaseConfirm
		}
		return m, nil

	case applyDoneMsg:
		m.results = msg.results
		m.applyErr = msg.err
		m.finishFromResults()
		m.phase = phaseDone
		m.finished = time.Now()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.cancelled = true
		m.cancel()
		return m, tea.Quit
	case "q", "esc":
		if m.phase == phaseDone {
			return m, tea.Quit
		}
		m.cancelled = true
		m.cancel()
		return m, tea.Quit
	case "y", "Y", "enter":
		switch m.phase {
		case phaseConfirm:
			m.confirmed = true
			m.phase = phaseRunning
			for _, r := range m.rows {
				if r.state == statePending {
					r.state = stateSubmitting
					r.since = time.Now()
				}
			}
			return m, m.runApply()
		case phaseDone:
			return m, tea.Quit
		}
	case "n", "N":
		if m.phase == phaseConfirm {
			m.cancelled = true
			m.cancel()
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *Model) setItems(items []Item) {
	m.rows = make([]*row, 0, len(items))
	m.byKey = make(map[string]*row, len(items))
	for _, it := range items {
		r := &row{item: it, state: statePending}
		m.rows = append(m.rows, r)
		m.byKey[it.Key] = r
	}
}

func (m *Model) pendingKeys() []string {
	var out []string
	for _, r := range m.rows {
		if r.state == statePending {
			out = append(out, r.item.Key)
		}
	}
	return out
}

// applyProgress folds one provider event into the UI state.
func (m *Model) applyProgress(ev cloud.Progress) {
	m.eventsSeen++
	switch ev.Key {
	case "progress.apply.item":
		m.markTargets(argString(ev.Args, "domain"), stateSubmitting)
	case "progress.apply.verifying":
		m.markTargets(argString(ev.Args, "domain"), stateWaiting)
	case "progress.apply.verified":
		m.markTargets(argString(ev.Args, "domain"), stateVerified)
	case "progress.apply.unverified":
		m.markTargets(argString(ev.Args, "domain"), stateUnverified)
		m.setStateText(argString(ev.Args, "domain"), argString(ev.Args, "state"))
	case "progress.apply.failed":
		m.markTargets(argString(ev.Args, "domain"), stateFailed)
		m.setStateText(argString(ev.Args, "domain"), argString(ev.Args, "code"))
	case "progress.apply.summary", "progress.delete.group":
		// Counters are derived from the rows; nothing to do.
	default:
		// Scan stages keep their own log so the first (slow) scan is visible.
		if strings.HasPrefix(ev.Key, "progress.") {
			m.scanLines = append(m.scanLines, m.bundle.T(ev.Key, ev.Args...))
			if len(m.scanLines) > 200 {
				m.scanLines = m.scanLines[len(m.scanLines)-200:]
			}
		}
	}
}

// markTargets updates every row whose domain appears in a (possibly batched) list.
func (m *Model) markTargets(domains string, state itemState) {
	for _, name := range strings.Split(domains, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, r := range m.rows {
			if strings.EqualFold(r.item.Domain, name) {
				r.state = state
				if state == stateWaiting || state == stateSubmitting {
					r.since = time.Now()
				}
			}
		}
	}
}

func (m *Model) setStateText(domains, text string) {
	if text == "" {
		return
	}
	for _, name := range strings.Split(domains, ",") {
		name = strings.TrimSpace(name)
		for _, r := range m.rows {
			if strings.EqualFold(r.item.Domain, name) {
				r.detail = text
			}
		}
	}
}

// finishFromResults reconciles rows with the authoritative results.
func (m *Model) finishFromResults() {
	for _, res := range m.results {
		r, ok := m.byKey[itemKey(string(res.Change.Product), res.Change.Domain)]
		if !ok {
			continue
		}
		switch {
		case !res.OK:
			r.state = stateFailed
		case !res.Verified:
			r.state = stateUnverified
		default:
			r.state = stateVerified
		}
		if res.ErrCode != "" {
			r.detail = res.ErrCode
		} else if res.MessageKey != "" {
			r.detail = m.bundle.T(res.MessageKey)
		}
	}
}

// counts returns the tallies shown in the header. "active" covers every row that
// is not finished yet (pending, submitting or waiting), which is what an operator
// wants to see while a run is in flight.
func (m *Model) counts() (ok, unconfirmed, failed, active int) {
	for _, r := range m.rows {
		switch r.state {
		case stateVerified:
			ok++
		case stateUnverified:
			unconfirmed++
		case stateFailed:
			failed++
		default:
			active++
		}
	}
	return
}

// pendingOnly counts rows that have not been submitted at all.
func (m *Model) pendingOnly() int {
	n := 0
	for _, r := range m.rows {
		if r.state == statePending {
			n++
		}
	}
	return n
}

// View implements tea.Model.
func (m *Model) View() string {
	width := m.width
	if width < 40 {
		width = 100
	}
	elapsed := time.Since(m.started)
	if m.phase == phaseDone && !m.finished.IsZero() {
		elapsed = m.finished.Sub(m.started)
	}

	head := m.styles.title.Render(m.bundle.T("tui.header", "title", m.job.Title, "elapsed", elapsed.Round(time.Second).String()))
	for _, line := range m.job.Summary {
		head += "\n" + m.styles.subtitle.Render(line)
	}
	header := panel(m.styles, "Orbit", head, width, false)

	switch m.phase {
	case phaseScanning:
		body := panel(m.styles, m.bundle.T("tui.scanning_panel"),
			m.spinner.View()+" "+m.bundle.T("tui.scanning")+"\n"+m.renderScanLog(), width, true)
		return screen(width, m.height, m.footer(), header, body)

	case phaseConfirm:
		rows := panel(m.styles, m.bundle.T("tui.plan_panel", "count", len(m.rows)),
			m.renderRows(), width, true)
		hint := m.styles.active.Render(m.bundle.T("tui.confirm_hint"))
		return screen(width, m.height, hint, header, rows)

	case phaseRunning:
		ok, unconfirmed, failed, active := m.counts()
		done, total := ok+unconfirmed+failed, len(m.rows)
		bar := progressBar(m.styles, done, total, 28) + "  " + m.styles.dim.Render(fmt.Sprintf("%d/%d", done, total))
		summary := bar + "  " + m.bundle.T("tui.running",
			"ok", ok, "unconfirmed", unconfirmed, "failed", failed, "active", active)
		rows := panel(m.styles, m.bundle.T("tui.plan_panel", "count", len(m.rows)),
			m.renderRows(), width, true)
		return screen(width, m.height, m.footer(), header, summary, rows)

	default:
		body := panel(m.styles, m.bundle.T("tui.result_panel"), m.renderOutcome(), width, true)
		return screen(width, m.height, m.styles.dim.Render(m.bundle.T("tui.exit_hint")), header, body)
	}
}

// footer is the key legend shown while a run is in flight.
func (m *Model) footer() string {
	return keyHints(m.styles, m.width, [][2]string{
		{"space", m.bundle.T("keys.toggle")},
		{"q", m.bundle.T("keys.quit")},
	})
}

func (m *Model) renderScanLog() string {
	var b strings.Builder
	lines := m.scanLines
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	for _, l := range lines {
		b.WriteString(m.styles.dim.Render(l))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) renderRows() string {
	rows := m.rows
	if len(rows) > m.maxRows() {
		rows = rows[:m.maxRows()]
	}
	var b strings.Builder
	for _, r := range rows {
		state := m.stateLabel(r)
		target := r.item.Note
		if r.item.Target != "" {
			target = r.item.Target
		}
		line := fmt.Sprintf("  %-3s %-6s %-40s %-12s %s",
			m.spinnerOrMark(r), strings.ToUpper(r.item.Product),
			truncate(r.item.Domain, 40), truncate(target, 12), state)
		if r.detail != "" {
			line += m.styles.dim.Render("  " + r.detail)
		}
		b.WriteString(line + "\n")
	}
	if hidden := len(m.rows) - len(rows); hidden > 0 {
		b.WriteString(m.styles.dim.Render(fmt.Sprintf("  ... +%d", hidden)) + "\n")
	}
	return b.String()
}

// spinnerOrMark animates the rows that are actively waiting for the cloud.
func (m *Model) spinnerOrMark(r *row) string {
	switch r.state {
	case stateVerified:
		return m.styles.ok.Render("✓")
	case stateFailed:
		return m.styles.fail.Render("✗")
	case stateUnverified:
		return m.styles.warn.Render("?")
	case stateWaiting, stateSubmitting:
		return m.spinner.View()
	default:
		return m.styles.dim.Render("·")
	}
}

func (m *Model) stateLabel(r *row) string {
	switch r.state {
	case stateVerified:
		return m.styles.ok.Render(m.bundle.T("tui.state.verified"))
	case stateFailed:
		return m.styles.fail.Render(m.bundle.T("tui.state.failed"))
	case stateUnverified:
		return m.styles.warn.Render(m.bundle.T("tui.state.unverified"))
	case stateWaiting:
		waited := time.Since(r.since).Round(time.Second)
		if r.since.IsZero() {
			waited = 0
		}
		return m.styles.active.Render(m.bundle.T("tui.state.waiting", "waited", waited.String()))
	case stateSubmitting:
		return m.styles.active.Render(m.bundle.T("tui.state.submitting"))
	default:
		return m.styles.dim.Render(m.bundle.T("tui.state.pending"))
	}
}

func (m *Model) renderOutcome() string {
	if m.scanErr != nil {
		return m.styles.fail.Render(m.bundle.T("tui.scan_failed", "error", m.scanErr.Error())) + "\n\n" +
			m.styles.dim.Render(m.bundle.T("tui.exit_hint"))
	}
	if m.cancelled {
		return m.styles.warn.Render(m.bundle.T("tui.cancelled")) + "\n\n" +
			m.styles.dim.Render(m.bundle.T("tui.exit_hint"))
	}
	ok, unconfirmed, failed, _ := m.counts()
	var b strings.Builder
	b.WriteString(m.bundle.T("tui.done", "ok", ok, "unconfirmed", unconfirmed, "failed", failed) + "\n")
	if m.applyErr != nil {
		b.WriteString(m.styles.fail.Render(m.applyErr.Error()) + "\n")
	}
	if ok+unconfirmed == 0 && failed == 0 {
		b.WriteString(m.styles.dim.Render(m.bundle.T("tui.nothing_to_do")) + "\n")
	}
	b.WriteString("\n" + m.renderRows())
	return b.String()
}

func (m *Model) maxRows() int {
	if m.height <= 0 {
		return 20
	}
	limit := m.height - 12
	if limit < 3 {
		limit = 3
	}
	return limit
}

// ------------------------------------------------------------------ helpers

func itemKey(product, domain string) string {
	return product + "|" + strings.ToLower(domain)
}

// NewItem builds a row with the canonical key.
func NewItem(product, domain, target, note string) Item {
	return Item{Key: itemKey(product, domain), Product: product, Domain: domain, Target: target, Note: note}
}

func argString(args []any, name string) string {
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok || key != name {
			continue
		}
		switch v := args[i+1].(type) {
		case string:
			return v
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// truncate cuts s to at most width display columns, appending an ellipsis when
// something was removed. Width is measured in terminal cells, so CJK text (two
// cells per rune) is handled correctly.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}
