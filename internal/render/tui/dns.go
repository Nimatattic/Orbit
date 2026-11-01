package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/i18n"
)

// DNSJob exposes the operations the DNS console needs. The console never talks to
// a cloud SDK itself, which keeps the model testable without a network.
type DNSJob struct {
	Account string
	Zones   func(ctx context.Context) ([]core.DNSZone, error)
	Records func(ctx context.Context, zone string) ([]core.DNSRecord, error)
	Create  func(ctx context.Context, in core.DNSRecordInput) (core.DNSRecord, error)
	Update  func(ctx context.Context, record core.DNSRecord) (core.DNSRecord, error)
	Delete  func(ctx context.Context, zone string, id uint64) error
	Toggle  func(ctx context.Context, zone string, id uint64, enabled bool) error
}

type dnsMode int

const (
	dnsBrowse dnsMode = iota
	dnsFormMode
	dnsConfirmDelete
	dnsFilterMode
)

// dnsForm is the create/edit sheet. Every value is a text field so the operator
// can type anything and get the same validation the CLI applies.
type dnsForm struct {
	labels  []string
	inputs  []textinput.Model
	focus   int
	editing *core.DNSRecord // nil when creating
	err     string
}

// dnsFormLabels are the field labels, in focus order.
var dnsFormFields = []string{"name", "type", "value", "ttl", "mx", "remark"}

func newDNSForm(bundle *i18n.Bundle, existing *core.DNSRecord) *dnsForm {
	f := &dnsForm{editing: existing}
	values := []string{"@", "A", "", strconv.FormatUint(core.DefaultDNSTTL, 10), "10", ""}
	if existing != nil {
		values = []string{
			existing.Name, existing.Type, existing.Value,
			strconv.FormatUint(existing.TTL, 10), strconv.FormatUint(existing.MX, 10), existing.Remark,
		}
	}
	for i, name := range dnsFormFields {
		in := textinput.New()
		in.Placeholder = ""
		in.SetValue(values[i])
		in.CharLimit = 512
		in.Width = 48
		if i == 0 {
			in.Focus()
		}
		f.labels = append(f.labels, bundle.T("dns.form."+name))
		f.inputs = append(f.inputs, in)
	}
	return f
}

// value returns the raw field text.
func (f *dnsForm) value(idx int) string { return strings.TrimSpace(f.inputs[idx].Value()) }

// focusTo moves the focus ring, wrapping at the ends.
func (f *dnsForm) focusTo(idx int) {
	if idx < 0 {
		idx = len(f.inputs) - 1
	}
	if idx >= len(f.inputs) {
		idx = 0
	}
	for i := range f.inputs {
		if i == idx {
			f.inputs[i].Focus()
			f.inputs[i].CursorEnd()
		} else {
			f.inputs[i].Blur()
		}
	}
	f.focus = idx
}

// input converts the form into a validated record input.
func (f *dnsForm) input(zone string) (core.DNSRecordInput, error) {
	ttl, err := strconv.ParseUint(f.value(3), 10, 64)
	if err != nil {
		return core.DNSRecordInput{}, fmt.Errorf("ttl must be a number")
	}
	mx := uint64(0)
	if v := f.value(4); v != "" {
		mx, err = strconv.ParseUint(v, 10, 64)
		if err != nil {
			return core.DNSRecordInput{}, fmt.Errorf("mx must be a number")
		}
	}
	in := core.DNSRecordInput{
		Zone: zone, Name: f.value(0), Type: strings.ToUpper(f.value(1)), Value: f.value(2),
		TTL: ttl, MX: mx, Remark: f.value(5), Enabled: true,
	}
	if f.editing != nil {
		in.Enabled = f.editing.Enabled
		in.Line = f.editing.Line
	}
	if err := core.ValidateRecordInput(in); err != nil {
		return core.DNSRecordInput{}, err
	}
	return in, nil
}

type dnsZonesMsg struct {
	zones []core.DNSZone
	err   error
}

type dnsRecordsMsg struct {
	zone    string
	records []core.DNSRecord
	err     error
}

type dnsWriteMsg struct {
	op     string
	fqdn   string
	record core.DNSRecord
	err    error
}

type dnsModel struct {
	bundle *i18n.Bundle
	job    DNSJob
	ctx    context.Context
	cancel context.CancelFunc
	styles styles
	spin   spinner.Model

	zones   []core.DNSZone
	records []core.DNSRecord
	zoneIdx int
	cursor  int
	filter  string
	loading bool
	err     error
	status  string
	mode    dnsMode

	form    *dnsForm
	pending *core.DNSRecord
	input   textinput.Model

	width  int
	height int
}

func newDNSModel(ctx context.Context, cancel context.CancelFunc, bundle *i18n.Bundle, job DNSJob, noColor bool) *dnsModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	filter := textinput.New()
	filter.Placeholder = ""
	filter.CharLimit = 64
	filter.Width = 40

	return &dnsModel{
		bundle: bundle, job: job, ctx: ctx, cancel: cancel,
		styles: newStyles(noColor), spin: sp, input: filter,
		width: 110, loading: true,
	}
}

// Init implements tea.Model.
func (m *dnsModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadZones())
}

func (m *dnsModel) loadZones() tea.Cmd {
	job, ctx := m.job, m.ctx
	return func() tea.Msg {
		zones, err := job.Zones(ctx)
		return dnsZonesMsg{zones: zones, err: err}
	}
}

func (m *dnsModel) loadRecords(zone string) tea.Cmd {
	job, ctx := m.job, m.ctx
	return func() tea.Msg {
		records, err := job.Records(ctx, zone)
		return dnsRecordsMsg{zone: zone, records: records, err: err}
	}
}

// currentZone returns the selected zone name, or "".
func (m *dnsModel) currentZone() string {
	if m.zoneIdx < 0 || m.zoneIdx >= len(m.zones) {
		return ""
	}
	return m.zones[m.zoneIdx].Name
}

// visibleRecords applies the filter and keeps a stable order.
func (m *dnsModel) visibleRecords() []core.DNSRecord {
	records := m.records
	if m.filter != "" {
		records = core.FilterDNSRecords(records, core.DNSRecordFilter{Keyword: m.filter})
	}
	out := make([]core.DNSRecord, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// selectedRecord returns the record under the cursor.
func (m *dnsModel) selectedRecord() *core.DNSRecord {
	records := m.visibleRecords()
	if m.cursor < 0 || m.cursor >= len(records) {
		return nil
	}
	r := records[m.cursor]
	return &r
}

// Update implements tea.Model.
func (m *dnsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case dnsZonesMsg:
		m.loading = false
		m.err = msg.err
		m.zones = msg.zones
		m.zoneIdx, m.cursor = 0, 0
		if msg.err != nil || len(msg.zones) == 0 {
			return m, nil
		}
		m.loading = true
		return m, m.loadRecords(m.currentZone())

	case dnsRecordsMsg:
		if msg.zone != m.currentZone() {
			return m, nil // a stale answer from a zone the operator already left
		}
		m.loading = false
		m.err = msg.err
		m.records = msg.records
		m.clampCursor()
		return m, nil

	case dnsWriteMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
			return m, nil
		}
		m.err = nil
		m.status = m.bundle.T("dns.tui."+msg.op, "fqdn", msg.fqdn)
		return m, m.loadRecords(m.currentZone())

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// clampCursor keeps the cursor inside the visible list.
func (m *dnsModel) clampCursor() {
	n := len(m.visibleRecords())
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *dnsModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case dnsFormMode:
		return m.updateForm(msg)
	case dnsConfirmDelete:
		return m.updateConfirm(msg)
	case dnsFilterMode:
		return m.updateFilter(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.cancel()
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < len(m.visibleRecords())-1 {
			m.cursor++
		}
		return m, nil
	case "left", "h", "shift+tab":
		return m.switchZone(-1)
	case "right", "l", "tab":
		return m.switchZone(1)
	case "n":
		m.form = newDNSForm(m.bundle, nil)
		m.mode = dnsFormMode
		return m, nil
	case "e":
		if rec := m.selectedRecord(); rec != nil {
			m.form = newDNSForm(m.bundle, rec)
			m.mode = dnsFormMode
		}
		return m, nil
	case "d":
		if rec := m.selectedRecord(); rec != nil {
			m.pending = rec
			m.mode = dnsConfirmDelete
		}
		return m, nil
	case " ":
		rec := m.selectedRecord()
		if rec == nil {
			return m, nil
		}
		job, ctx, zone := m.job, m.ctx, rec.Zone
		id, enabled := rec.ID, !rec.Enabled
		fqdn := rec.FQDN()
		op := "disabled"
		if enabled {
			op = "enabled"
		}
		return m, func() tea.Msg {
			err := job.Toggle(ctx, zone, id, enabled)
			return dnsWriteMsg{op: op, fqdn: fqdn, err: err}
		}
	case "r":
		m.loading = true
		return m, m.loadRecords(m.currentZone())
	case "/":
		m.input.SetValue(m.filter)
		m.input.Focus()
		m.mode = dnsFilterMode
		return m, nil
	}
	return m, nil
}

// switchZone moves the zone selection and reloads its records.
func (m *dnsModel) switchZone(delta int) (tea.Model, tea.Cmd) {
	if len(m.zones) == 0 {
		return m, nil
	}
	next := (m.zoneIdx + delta + len(m.zones)) % len(m.zones)
	if next == m.zoneIdx {
		return m, nil
	}
	m.zoneIdx, m.cursor, m.filter = next, 0, ""
	m.records = nil
	m.loading = true
	return m, m.loadRecords(m.currentZone())
}

// updateForm handles the create/edit sheet.
func (m *dnsModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	form := m.form
	switch msg.String() {
	case "esc":
		m.mode, m.form = dnsBrowse, nil
		return m, nil
	case "tab", "down":
		form.focusTo(form.focus + 1)
		return m, nil
	case "shift+tab", "up":
		form.focusTo(form.focus - 1)
		return m, nil
	case "enter":
		zone := m.currentZone()
		in, err := form.input(zone)
		if err != nil {
			form.err = err.Error()
			return m, nil
		}
		job, ctx := m.job, m.ctx
		if form.editing != nil {
			desired := *form.editing
			desired.Name, desired.Type, desired.Value = in.Name, in.Type, in.Value
			desired.TTL, desired.MX, desired.Remark, desired.Enabled = in.TTL, in.MX, in.Remark, in.Enabled
			fqdn := desired.FQDN()
			m.mode, m.form = dnsBrowse, nil
			return m, func() tea.Msg {
				_, err := job.Update(ctx, desired)
				return dnsWriteMsg{op: "updated", fqdn: fqdn, err: err}
			}
		}
		fqdn := core.RecordFQDN(zone, in.Name)
		m.mode, m.form = dnsBrowse, nil
		return m, func() tea.Msg {
			_, err := job.Create(ctx, in)
			return dnsWriteMsg{op: "created", fqdn: fqdn, err: err}
		}
	}
	var cmd tea.Cmd
	form.inputs[form.focus], cmd = form.inputs[form.focus].Update(msg)
	form.err = ""
	return m, cmd
}

// updateConfirm handles the delete confirmation.
func (m *dnsModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		rec := m.pending
		m.mode, m.pending = dnsBrowse, nil
		if rec == nil {
			return m, nil
		}
		job, ctx, fqdn, zone, id := m.job, m.ctx, rec.FQDN(), rec.Zone, rec.ID
		return m, func() tea.Msg {
			err := job.Delete(ctx, zone, id)
			return dnsWriteMsg{op: "deleted", fqdn: fqdn, err: err}
		}
	case "n", "N", "esc":
		m.mode, m.pending = dnsBrowse, nil
	}
	return m, nil
}

// updateFilter handles the / filter input.
func (m *dnsModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter = ""
		m.input.SetValue("")
		m.input.Blur()
		m.mode, m.cursor = dnsBrowse, 0
		return m, nil
	case "enter":
		m.filter = strings.TrimSpace(m.input.Value())
		m.input.Blur()
		m.mode, m.cursor = dnsBrowse, 0
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View implements tea.Model.
func (m *dnsModel) View() string {
	width := m.width
	if width < 60 {
		width = 110
	}

	head := m.styles.subtitle.Render(m.bundle.T("dns.tui.subtitle", "account", m.job.Account))
	header := panel(m.styles, "Orbit · "+m.bundle.T("dns.tui.title"), head, width, false)

	if m.err != nil && len(m.zones) == 0 {
		body := panel(m.styles, m.bundle.T("dns.tui.zones"),
			m.styles.fail.Render(m.bundle.T("dns.account_failed", "account", m.job.Account, "error", m.err.Error())),
			width, true)
		return screen(width, m.height, m.footer(), header, body)
	}

	blocks := []string{header, m.zonePanel(width), m.recordPanel(width)}
	if panel := m.overlayPanel(width); panel != "" {
		blocks = append(blocks, panel)
	}
	if m.status != "" {
		blocks = append(blocks, m.styles.ok.Render(m.status))
	} else if m.err != nil {
		blocks = append(blocks, m.styles.fail.Render(m.err.Error()))
	}
	return screen(width, m.height, m.footer(), blocks...)
}

func (m *dnsModel) zonePanel(width int) string {
	var b strings.Builder
	if len(m.zones) == 0 {
		b.WriteString(m.styles.dim.Render(m.bundle.T("dns.no_zones")))
	}
	for i, z := range m.zones {
		cursor := "  "
		if i == m.zoneIdx {
			cursor = "▸ "
		}
		line := fmt.Sprintf("%s%-28s %-8d %s", cursor, z.Name, z.RecordCount, m.styles.dim.Render(z.Grade))
		if i == m.zoneIdx {
			line = m.styles.active.Render(line)
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	return panel(m.styles, m.bundle.T("dns.tui.zones"), truncateLines(b.String(), width-2), width, true)
}

func (m *dnsModel) recordPanel(width int) string {
	records := m.visibleRecords()
	title := m.bundle.T("dns.tui.records", "zone", m.currentZone(), "count", len(records))
	if m.filter != "" {
		title += "  /" + m.filter
	}
	if m.loading {
		return panel(m.styles, title, m.spin.View()+" "+m.bundle.T("dns.tui.loading"), width, true)
	}
	if len(records) == 0 {
		return panel(m.styles, title, m.styles.dim.Render(m.bundle.T("dns.no_records", "zone", m.currentZone())), width, true)
	}

	rows := m.recordRows()
	cursor := m.cursor
	start := 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	end := start + rows
	if end > len(records) {
		end = len(records)
	}

	var b strings.Builder
	b.WriteString(m.styles.dim.Render(fmt.Sprintf("%-3s %-7s %-22s %-6s %-34s %-6s %s",
		"", "ID", m.bundle.T("dns.col_name"), m.bundle.T("dns.col_type"), m.bundle.T("dns.col_value"),
		"TTL", m.bundle.T("dns.col_state"))))
	for i := start; i < end; i++ {
		r := records[i]
		marker := "   "
		if i == cursor {
			marker = "▸  "
		}
		state := m.styles.ok.Render("enabled")
		if !r.Enabled {
			state = m.styles.dim.Render("disabled")
		}
		line := fmt.Sprintf("%s%-7d %-22s %-6s %-34s %-6d ", marker, r.ID, r.Name, r.Type, truncate(r.Value, 34), r.TTL) + state
		if i == cursor {
			line = m.styles.active.Render(fmt.Sprintf("%s%-7d %-22s %-6s %-34s %-6d %s",
				marker, r.ID, r.Name, r.Type, truncate(r.Value, 34), r.TTL, dnsStateText(r.Enabled)))
		}
		b.WriteString("\n" + line)
	}
	return panel(m.styles, title, truncateLines(b.String(), width-2), width, true)
}

// overlayPanel renders whichever modal is open.
func (m *dnsModel) overlayPanel(width int) string {
	switch m.mode {
	case dnsFormMode:
		var b strings.Builder
		for i, in := range m.form.inputs {
			label := m.form.labels[i]
			cursor := "  "
			if i == m.form.focus {
				cursor = "▸ "
			}
			line := fmt.Sprintf("%s%-8s %s", cursor, label, in.View())
			if i == m.form.focus {
				line = m.styles.active.Render(fmt.Sprintf("%s%-8s ", cursor, label)) + in.View()
			}
			b.WriteString(line)
			if i < len(m.form.inputs)-1 {
				b.WriteString("\n")
			}
		}
		title := m.bundle.T("dns.tui.form_new")
		if m.form.editing != nil {
			title = m.bundle.T("dns.tui.form_edit", "id", m.form.editing.ID)
		}
		if m.form.err != "" {
			b.WriteString("\n" + m.styles.fail.Render(m.form.err))
		}
		return panel(m.styles, title, truncateLines(b.String(), width-2), width, true)
	case dnsConfirmDelete:
		if m.pending == nil {
			return ""
		}
		body := m.bundle.T("dns.tui.confirm_delete", "fqdn", m.pending.FQDN(), "type", m.pending.Type, "value", m.pending.Value)
		return panel(m.styles, m.bundle.T("dns.tui.confirm_title"), m.styles.warn.Render(body), width, true)
	case dnsFilterMode:
		body := m.styles.active.Render(m.bundle.T("dns.tui.filter_label")) + " " + m.input.View()
		return panel(m.styles, m.bundle.T("dns.tui.filter_title"), body, width, true)
	}
	return ""
}

func (m *dnsModel) recordRows() int {
	rows := m.height - 22
	if rows < 3 {
		rows = 3
	}
	if m.mode != dnsBrowse {
		rows -= 3
	}
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m *dnsModel) footer() string {
	if m.mode == dnsFormMode {
		return keyHints(m.styles, m.width, [][2]string{
			{"tab", m.bundle.T("keys.move")},
			{"enter", m.bundle.T("keys.save_confirm")},
			{"esc", m.bundle.T("keys.cancel")},
		})
	}
	if m.mode == dnsConfirmDelete {
		return keyHints(m.styles, m.width, [][2]string{
			{"y", m.bundle.T("keys.save_confirm")},
			{"n", m.bundle.T("keys.cancel")},
		})
	}
	if m.mode == dnsFilterMode {
		return keyHints(m.styles, m.width, [][2]string{
			{"enter", m.bundle.T("dns.tui.key_apply")},
			{"esc", m.bundle.T("keys.cancel")},
		})
	}
	return keyHints(m.styles, m.width, [][2]string{
		{"↑/↓", m.bundle.T("keys.move")},
		{"←/→", m.bundle.T("dns.tui.key_zone")},
		{"n", m.bundle.T("dns.tui.key_new")},
		{"e", m.bundle.T("dns.tui.key_edit")},
		{"d", m.bundle.T("dns.tui.key_delete")},
		{"space", m.bundle.T("dns.tui.key_toggle")},
		{"/", m.bundle.T("dns.tui.key_filter")},
		{"r", m.bundle.T("keys.rescan")},
		{"q", m.bundle.T("keys.quit")},
	})
}

// dnsStateText renders the enabled flag for the cursor row.
func dnsStateText(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// RunDNS opens the DNS console until the operator quits.
func RunDNS(ctx context.Context, bundle *i18n.Bundle, job DNSJob) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	noColor := os.Getenv("NO_COLOR") != ""
	model := newDNSModel(runCtx, cancel, bundle, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	_, err := program.Run()
	return err
}
