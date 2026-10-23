package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Crequency/orbit/internal/i18n"
)

// GuidePermission is one CAM action shown by the permission guide.
type GuidePermission struct {
	Action string
	// Tier is read, write or delete; used for grouping and colour.
	Tier string
	// Purpose is already localised by the caller.
	Purpose string
	// Experimental marks actions only needed by the experimental deploy strategy.
	Experimental bool
	// NoGrant marks an API that needs no CAM authorization; it is listed for
	// completeness but never written into the policy.
	NoGrant bool
}

// GuidePlatform is a cloud vendor the operator can choose.
type GuidePlatform struct {
	ID          string
	Label       string
	Available   bool
	Unavailable string // reason shown when not selectable yet
}

// GuideProduct is a product scope the operator can opt into.
type GuideProduct struct {
	ID       string
	Label    string
	Required bool
}

// GuideScope is one usage level, ordered from least to most authority.
type GuideScope struct {
	ID    string
	Label string
	Note  string
}

// GuideJob describes the guide screen.
type GuideJob struct {
	Platforms []GuidePlatform
	Products  []GuideProduct
	Scopes    []GuideScope
	// InitialProducts and InitialScope pre-select the flags the operator passed;
	// empty means "everything / the safest level".
	InitialProducts []string
	InitialScope    string
	// Permissions recomputes the list for the current selection.
	Permissions func(products []string, scopeID string, experimental bool) []GuidePermission
	// PolicyJSON renders the custom policy for the current selection: the text
	// the operator pastes into the console's "按策略语法创建" editor.
	PolicyJSON func(products []string, scopeID string, experimental bool) string
	// PolicyName suggests a console name for the current selection.
	PolicyName func(products []string, scopeID string) string
	// PolicySteps are the console steps, already localised.
	PolicySteps []string
	// PolicySources are the official CAM interface lists, for spot-checking.
	PolicySources []string
	// SaveDefault is the path offered when the operator saves the policy.
	SaveDefault string
	// ServiceRole is the linked role the write/delete scopes need.
	ServiceRole string
	// PolicyHint tells the operator where a paste-ready policy lives.
	PolicyHint string
}

// actionWidth is the column width reserved for the CAM action name.
const actionWidth = 42

// Sections the cursor can move through.
const (
	guideSectionPlatform = iota
	guideSectionProducts
	guideSectionScope
	guideSectionList
	guideSectionPolicy
	guideSectionCount
)

type guideModel struct {
	bundle *i18n.Bundle
	job    GuideJob

	platform      int
	productCursor int
	products      map[string]bool
	scope         int
	scroll        int
	policyScroll  int
	showPolicy    bool

	input     textinput.Model
	saving    bool
	savedPath string
	saveMsg   string
	saveErr   string

	focus  int
	width  int
	height int
	styles styles
}

func newGuideModel(bundle *i18n.Bundle, job GuideJob, noColor bool) *guideModel {
	products := map[string]bool{}
	if len(job.InitialProducts) == 0 {
		// Everything starts enabled: the guide answers "what would I need".
		for _, p := range job.Products {
			products[p.ID] = true
		}
	} else {
		wanted := map[string]bool{}
		for _, id := range job.InitialProducts {
			wanted[id] = true
		}
		for _, p := range job.Products {
			// Required products cannot be pre-deselected.
			products[p.ID] = wanted[p.ID] || p.Required
		}
	}
	saveDefault := job.SaveDefault
	if saveDefault == "" {
		saveDefault = defaultPolicyFile
	}
	input := textinput.New()
	input.Placeholder = saveDefault
	input.SetValue(saveDefault)
	input.CharLimit = 512
	input.Prompt = "> "
	input.Width = 60

	m := &guideModel{
		bundle: bundle, job: job, products: products,
		width: 100, styles: newStyles(noColor), input: input,
	}
	for i, s := range job.Scopes {
		if s.ID == job.InitialScope {
			m.scope = i
		}
	}
	return m
}

// Init implements tea.Model.
func (m *guideModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *guideModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = clamp(msg.Width-24, 20, 120)
		return m, nil

	case tea.KeyMsg:
		if m.saving {
			return m.updateSaving(msg)
		}
		switch msg.String() {
		case "p":
			m.showPolicy = !m.showPolicy
			if m.showPolicy {
				m.focus = guideSectionPolicy
				m.policyScroll = 0
			} else if m.focus == guideSectionPolicy {
				m.focus = guideSectionList
			}
			return m, nil
		case "w":
			if m.showPolicy {
				m.startSaving()
			}
			return m, nil
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.showPolicy {
				// Esc leaves the policy view instead of quitting: the operator is
				// one keypress away from the configuration they just built.
				m.showPolicy = false
				m.focus = guideSectionList
				return m, nil
			}
			return m, tea.Quit
		case "tab", "s":
			m.cycleFocus(1)
			return m, nil
		case "shift+tab":
			m.cycleFocus(-1)
			return m, nil
		case "up", "k":
			m.move(-1)
			return m, nil
		case "down", "j":
			m.move(1)
			return m, nil
		case " ":
			m.toggle()
			return m, nil
		}
	}
	return m, nil
}

func (m *guideModel) move(delta int) {
	switch m.focus {
	case guideSectionPlatform:
		m.platform = clamp(m.platform+delta, 0, len(m.job.Platforms)-1)
	case guideSectionProducts:
		m.productCursor = clamp(m.productCursor+delta, 0, len(m.job.Products)-1)
	case guideSectionScope:
		m.scope = clamp(m.scope+delta, 0, len(m.job.Scopes)-1)
	case guideSectionList:
		m.scroll = clamp(m.scroll+delta, 0, maxInt(0, len(m.permissions())-m.listRows()))
	case guideSectionPolicy:
		m.policyScroll = clamp(m.policyScroll+delta, 0, maxInt(0, len(m.policyLines())-m.policyRows()))
	}
}

// cycleFocus moves through the sections, skipping the policy panel while hidden.
func (m *guideModel) cycleFocus(delta int) {
	for i := 0; i < guideSectionCount; i++ {
		m.focus = (m.focus + delta + guideSectionCount) % guideSectionCount
		if m.focus != guideSectionPolicy || m.showPolicy {
			return
		}
	}
}

func (m *guideModel) toggle() {
	switch m.focus {
	case guideSectionPlatform:
		// Single choice today, but keep the interaction symmetrical.
		return
	case guideSectionProducts:
		if len(m.job.Products) == 0 {
			return
		}
		p := m.job.Products[m.productCursor]
		if p.Required {
			// Required products cannot be switched off: they are what Orbit is.
			return
		}
		m.products[p.ID] = !m.products[p.ID]
	case guideSectionScope:
		return
	}
}

// selectedProducts returns the enabled scope ids in display order.
func (m *guideModel) selectedProducts() []string {
	out := make([]string, 0, len(m.job.Products))
	for _, p := range m.job.Products {
		if m.products[p.ID] {
			out = append(out, p.ID)
		}
	}
	return out
}

func (m *guideModel) permissions() []GuidePermission {
	if m.job.Permissions == nil {
		return nil
	}
	scope := ""
	if m.scope < len(m.job.Scopes) {
		scope = m.job.Scopes[m.scope].ID
	}
	return m.job.Permissions(m.selectedProducts(), scope, false)
}

// View implements tea.Model.
func (m *guideModel) View() string {
	width := m.width
	if width < 40 {
		width = 100
	}

	header := panel(m.styles, "Orbit",
		m.styles.title.Render(m.bundle.T("guide.title"))+"\n"+
			m.styles.subtitle.Render(m.bundle.T("guide.subtitle")), width, false)

	if m.showPolicy {
		// The policy gets its own view: the JSON must be fully visible and
		// scrollable, which is impossible if it shares the screen with the
		// configuration panels.
		stepLines := append([]string{}, m.job.PolicySteps...)
		if len(m.job.PolicySources) > 0 {
			stepLines = append(stepLines, "", m.bundle.T("guide.policy_sources"))
			stepLines = append(stepLines, m.job.PolicySources...)
		}
		steps := panel(m.styles, m.bundle.T("guide.policy_steps"),
			strings.Join(stepLines, "\n"), width, false)
		blocks := []string{header, steps, m.policyPanel(width)}
		if save := m.savePanel(width); save != "" {
			blocks = append(blocks, save)
		}
		return screen(width, m.height, m.policyFooter(), blocks...)
	}

	perms := m.permissions()
	blocks := []string{
		header,
		m.platformPanel(width),
		m.productPanel(width),
		m.scopePanel(width),
		m.permissionPanel(width, perms),
	}
	if m.requiresRole() {
		blocks = append(blocks, m.rolePanel(width))
	}
	return screen(width, m.height, m.footer(), blocks...)
}

func (m *guideModel) platformPanel(width int) string {
	var b strings.Builder
	for i, p := range m.job.Platforms {
		cursor := "  "
		if m.focus == guideSectionPlatform && i == m.platform {
			cursor = "▸ "
		}
		mark := "( )"
		if i == m.platform {
			mark = m.styles.active.Render("(•)")
		}
		line := fmt.Sprintf("%s%s %s", cursor, mark, p.Label)
		if !p.Available && p.Unavailable != "" {
			line += m.styles.dim.Render("  " + p.Unavailable)
		}
		if m.focus == guideSectionPlatform && i == m.platform {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line)
		if i < len(m.job.Platforms)-1 {
			b.WriteString("\n")
		}
	}
	return panel(m.styles, m.bundle.T("guide.step_platform"), b.String(), width, m.focus == guideSectionPlatform)
}

func (m *guideModel) productPanel(width int) string {
	var b strings.Builder
	for i, p := range m.job.Products {
		cursor := "  "
		if m.focus == guideSectionProducts && i == m.productCursor {
			cursor = "▸ "
		}
		box := m.styles.dim.Render("[ ]")
		if m.products[p.ID] {
			box = m.styles.ok.Render("[x]")
		}
		label := p.Label
		if p.Required {
			label += m.styles.dim.Render("  " + m.bundle.T("guide.required"))
		}
		line := fmt.Sprintf("%s%s %s", cursor, box, label)
		if m.focus == guideSectionProducts && i == m.productCursor {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line)
		if i < len(m.job.Products)-1 {
			b.WriteString("\n")
		}
	}
	return panel(m.styles, m.bundle.T("guide.step_products"), b.String(), width, m.focus == guideSectionProducts)
}

func (m *guideModel) scopePanel(width int) string {
	var b strings.Builder
	for i, s := range m.job.Scopes {
		cursor := "  "
		if m.focus == guideSectionScope && i == m.scope {
			cursor = "▸ "
		}
		mark := "( )"
		if i == m.scope {
			mark = m.styles.active.Render("(•)")
		}
		line := fmt.Sprintf("%s%s %s", cursor, mark, s.Label)
		if s.Note != "" {
			line += m.styles.dim.Render("  " + s.Note)
		}
		if m.focus == guideSectionScope && i == m.scope {
			line = m.styles.active.Render(line)
		}
		b.WriteString(line)
		if i < len(m.job.Scopes)-1 {
			b.WriteString("\n")
		}
	}
	return panel(m.styles, m.bundle.T("guide.step_scope"), b.String(), width, m.focus == guideSectionScope)
}

func (m *guideModel) permissionPanel(width int, perms []GuidePermission) string {
	if len(perms) == 0 {
		return panel(m.styles, m.bundle.T("guide.permissions"), m.styles.dim.Render(m.bundle.T("status.none")), width, m.focus == guideSectionList)
	}

	rows := m.listRows()
	scroll := clamp(m.scroll, 0, maxInt(0, len(perms)-rows))
	m.scroll = scroll
	end := scroll + rows
	if end > len(perms) {
		end = len(perms)
	}

	var b strings.Builder
	for i := scroll; i < end; i++ {
		p := perms[i]
		tier := m.styles.dim.Render(fmt.Sprintf("%-7s", p.Tier))
		switch p.Tier {
		case "read":
			tier = m.styles.ok.Render(fmt.Sprintf("%-7s", p.Tier))
		case "write":
			tier = m.styles.warn.Render(fmt.Sprintf("%-7s", p.Tier))
		case "delete":
			tier = m.styles.fail.Render(fmt.Sprintf("%-7s", p.Tier))
		}
		// The no-grant marker sits right after the action so a long purpose
		// cannot push it out of the panel.
		marker := ""
		if p.NoGrant {
			marker = m.styles.warn.Render("(" + m.bundle.T("guide.no_grant") + ") ")
		}
		// One row per permission: truncate instead of letting lipgloss wrap the
		// text, which would break the table on narrow terminals.
		inner := width - 2
		action := fmt.Sprintf("%-*s", actionWidth, truncate(p.Action, actionWidth))
		purposeWidth := inner - 9 - actionWidth - lipgloss.Width(marker)
		if purposeWidth < 8 {
			purposeWidth = 8
		}
		line := tier + " " + action + " " + marker + m.styles.dim.Render(truncate(p.Purpose, purposeWidth))
		if p.Experimental {
			line += m.styles.dim.Render(" (" + m.bundle.T("guide.experimental") + ")")
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(perms) {
		b.WriteString("\n" + m.styles.dim.Render(m.bundle.T("guide.more", "count", len(perms)-end)))
	}
	title := m.bundle.T("guide.permissions_count", "count", len(perms))
	return panel(m.styles, title, b.String(), width, m.focus == guideSectionList)
}

func (m *guideModel) rolePanel(width int) string {
	body := m.styles.subtitle.Render(m.job.ServiceRole)
	if m.job.PolicyHint != "" {
		body += "\n" + m.styles.dim.Render(m.job.PolicyHint)
	}
	return panel(m.styles, m.bundle.T("guide.extra_requirements"), body, width, false)
}

// policyText is the JSON for the current selection.
func (m *guideModel) policyText() string {
	if m.job.PolicyJSON == nil {
		return ""
	}
	scope := ""
	if m.scope < len(m.job.Scopes) {
		scope = m.job.Scopes[m.scope].ID
	}
	return m.job.PolicyJSON(m.selectedProducts(), scope, false)
}

// policyLines is the JSON, wrapped to the panel width.
func (m *guideModel) policyLines() []string {
	width := m.windowWidth() - 6
	if width < 20 {
		width = 20
	}
	body := strings.TrimRight(m.policyText(), "\n")
	if body == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return out
}

func (m *guideModel) policyPanel(width int) string {
	lines := m.policyLines()
	if len(lines) == 0 {
		return panel(m.styles, m.bundle.T("guide.policy_title"), m.bundle.T("status.none"), width, m.focus == guideSectionPolicy)
	}
	rows := m.policyRows()
	m.policyScroll = clamp(m.policyScroll, 0, maxInt(0, len(lines)-rows))
	end := m.policyScroll + rows
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := m.policyScroll; i < end; i++ {
		b.WriteString(lines[i])
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(lines) {
		b.WriteString("\n" + m.styles.dim.Render(m.bundle.T("guide.more", "count", len(lines)-end)))
	}
	title := m.bundle.T("guide.policy_title")
	if m.job.PolicyName != nil {
		scope := ""
		if m.scope < len(m.job.Scopes) {
			scope = m.job.Scopes[m.scope].ID
		}
		if name := m.job.PolicyName(m.selectedProducts(), scope); name != "" {
			title += "  " + name
		}
	}
	return panel(m.styles, title, b.String(), width, m.focus == guideSectionPolicy)
}

func (m *guideModel) policyRows() int {
	if m.height <= 0 {
		return 20
	}
	// header(3) + steps(3+lines+sources) + policy borders(2) + footer(1)
	steps := len(m.job.PolicySteps)
	if len(m.job.PolicySources) > 0 {
		steps += len(m.job.PolicySources) + 2
	}
	rows := m.height - 9 - steps
	if m.saving || m.saveMsg != "" || m.saveErr != "" {
		rows -= 4 // the save panel
	}
	if rows < 4 {
		rows = 4
	}
	return rows
}

func (m *guideModel) windowWidth() int {
	if m.width < 40 {
		return 100
	}
	return m.width
}

func (m *guideModel) requiresRole() bool {
	// The role is only needed once the operator grants write or delete authority.
	return m.scope > 0
}

func (m *guideModel) listRows() int {
	if m.height <= 0 {
		return 6
	}
	// Chrome: header(3) + platform(3-4) + products(3+n) + scope(3+3) + list borders(2)
	// + footer(1). Leave a little slack for the optional role panel.
	rows := m.height - 22
	if rows < 3 {
		rows = 3
	}
	if rows > 16 {
		rows = 16
	}
	return rows
}

// defaultPolicyFile is the offered file name when saving the policy.
const defaultPolicyFile = "orbit-account-policy.json"

// startSaving opens the path prompt, pre-filled with the default (or the path
// used before, which is what an operator repeating the action expects).
func (m *guideModel) startSaving() {
	if m.savedPath != "" {
		m.input.SetValue(m.savedPath)
	} else if strings.TrimSpace(m.input.Value()) == "" {
		m.input.SetValue(defaultPolicyFile)
	}
	m.input.CursorEnd()
	m.input.Focus()
	m.saving = true
	m.saveErr = ""
	m.saveMsg = ""
}

// updateSaving handles keys while the path prompt is open: everything goes to the
// input so paths containing q/p/w can be typed.
func (m *guideModel) updateSaving(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.saving = false
		m.input.Blur()
		m.saveErr = ""
		return m, nil
	case "enter":
		if err := m.savePolicy(); err != nil {
			m.saveErr = err.Error()
			m.saveMsg = ""
			return m, nil
		}
		m.saving = false
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.saveErr = ""
	return m, cmd
}

// savePolicy writes the JSON to the entered path.
func (m *guideModel) savePolicy() error {
	raw := strings.TrimSpace(m.input.Value())
	if raw == "" {
		return errors.New(m.bundle.T("guide.save_empty"))
	}
	target, err := resolveSavePath(raw, defaultPolicyFile)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(target); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// The policy is not a credential, but it does describe the account's access
	// posture, so it is written 0600 like the other generated artefacts.
	if err := os.WriteFile(target, []byte(m.policyText()), 0o600); err != nil {
		return err
	}
	m.savedPath = target
	m.saveMsg = m.bundle.T("guide.saved", "path", target)
	return nil
}

// resolveSavePath turns operator input into an absolute file path. A trailing
// separator (or an existing directory) means "save into this directory".
func resolveSavePath(raw, defaultName string) (string, error) {
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		raw = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(raw, "~"), string(os.PathSeparator)))
	}
	if strings.HasSuffix(raw, string(os.PathSeparator)) {
		raw = filepath.Join(raw, defaultName)
	} else if info, err := os.Stat(raw); err == nil && info.IsDir() {
		raw = filepath.Join(raw, defaultName)
	}
	return filepath.Abs(raw)
}

// savePanel renders the path prompt or the result of the last save.
func (m *guideModel) savePanel(width int) string {
	switch {
	case m.saving:
		body := m.styles.active.Render(m.bundle.T("guide.save_prompt")) + " " + m.input.View()
		if m.saveErr != "" {
			body += "\n" + m.styles.fail.Render(m.saveErr)
		}
		return panel(m.styles, m.bundle.T("guide.save_title"), body, width, true)
	case m.saveMsg != "":
		return panel(m.styles, m.bundle.T("guide.save_title"), m.styles.ok.Render(m.saveMsg), width, false)
	case m.saveErr != "":
		return panel(m.styles, m.bundle.T("guide.save_title"), m.styles.fail.Render(m.saveErr), width, false)
	}
	return ""
}

// policyFooter describes the policy view keys.
func (m *guideModel) policyFooter() string {
	if m.saving {
		return keyHints(m.styles, m.windowWidth(), [][2]string{
			{"enter", m.bundle.T("keys.save_confirm")},
			{"esc", m.bundle.T("keys.cancel")},
		})
	}
	return keyHints(m.styles, m.windowWidth(), [][2]string{
		{"↑/↓", m.bundle.T("keys.move")},
		{"w", m.bundle.T("keys.save")},
		{"p", m.bundle.T("keys.back")},
		{"q", m.bundle.T("keys.quit")},
	})
}

func (m *guideModel) footer() string {
	policyKey := m.bundle.T("keys.show_policy")
	if m.showPolicy {
		policyKey = m.bundle.T("keys.hide_policy")
	}
	return keyHints(m.styles, m.width, [][2]string{
		{"tab", m.bundle.T("keys.section")},
		{"↑/↓", m.bundle.T("keys.move")},
		{"space", m.bundle.T("keys.toggle")},
		{"p", policyKey},
		{"q", m.bundle.T("keys.quit")},
	})
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// RunGuide shows the permission guide until the operator quits.
func RunGuide(ctx context.Context, bundle *i18n.Bundle, job GuideJob) error {
	noColor := os.Getenv("NO_COLOR") != ""
	model := newGuideModel(bundle, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	_, err := program.Run()
	return err
}
