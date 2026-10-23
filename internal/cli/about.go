package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/config"
	"github.com/Crequency/orbit/internal/tty"
	"github.com/Crequency/orbit/internal/version"
)

// logoMark is the orbital glyph and logoWord the wordmark. Both are drawn by
// `orbit about` and shown in the README; TestAboutLogoMatchesREADME keeps the two
// copies identical.
var (
	logoMark = []string{
		"     .-~~~~~~~-.     ",
		"  .-~           ~-.  ",
		" /                 \\ ",
		"|         o         |",
		" \\                 / ",
		"  ~-.           .-~  ",
		"     `-~~~~~~~-'     ",
	}
	logoWord = []string{
		"  ___  ____  ____  ___ _____ ",
		" / _ \\|  _ \\| __ )|_ _|_   _|",
		"| | | | |_) |  _ \\ | |  | |  ",
		"| |_| |  _ <| |_) || |  | |  ",
		" \\___/|_| \\_\\____/|___|  |_| ",
	}
)

// ANSI codes, applied only on a colour-capable terminal.
const (
	ansiReset  = "\x1b[0m"
	ansiAccent = "\x1b[1;36m"
	ansiDim    = "\x1b[2m"
)

// newAboutCmd builds `orbit about`: a fastfetch-style identity card.
func (a *App) newAboutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: a.Bundle.T("cmd.about.short"),
		Long:  a.Bundle.T("cmd.about.long"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAbout()
		},
	}
}

// aboutRow is one label/value pair of the identity card.
type aboutRow struct {
	label string
	value string
}

// runAbout draws the logo next to build, environment and configuration facts.
func (a *App) runAbout() error {
	color := tty.IsTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""

	paint := func(s, code string) string {
		if !color || code == "" {
			return s
		}
		return code + s + ansiReset
	}

	title := paint("Orbit "+version.Version, ansiAccent)
	subtitle := paint(version.Commit+" · "+version.Date, ansiDim)

	rows := a.aboutRows()
	// Labels may be CJK, so pad by display width, not byte length.
	labelWidth := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.label); w > labelWidth {
			labelWidth = w
		}
	}

	// Left column: the glyph, then the wordmark, both centred in one width.
	const column = 29
	left := make([]string, 0, len(logoMark)+len(logoWord)+2)
	left = append(left, logoMark...)
	left = append(left, "")
	left = append(left, logoWord...)
	centred := make([]string, 0, len(left))
	for _, line := range left {
		pad := (column - len([]rune(line))) / 2
		if pad < 0 {
			pad = 0
		}
		centred = append(centred, strings.Repeat(" ", pad)+line)
	}

	// Right column: title, version, rule, then the labelled facts.
	right := []string{title, subtitle, paint(strings.Repeat("─", 42), ansiDim)}
	for _, r := range rows {
		pad := labelWidth - lipgloss.Width(r.label)
		if pad < 0 {
			pad = 0
		}
		right = append(right, paint(r.label+strings.Repeat(" ", pad), ansiDim)+"  "+r.value)
	}

	height := len(centred)
	if len(right) > height {
		height = len(right)
	}
	const gutter = "  "
	var b strings.Builder
	for i := 0; i < height; i++ {
		var leftPart string
		if i < len(centred) {
			leftPart = paint(centred[i], ansiAccent)
		}
		var rightPart string
		if i < len(right) {
			rightPart = right[i]
		}
		// Pad the left column by raw width so the right column stays aligned even
		// though the left side carries ANSI codes.
		padding := column
		if i < len(centred) {
			padding = column - len([]rune(centred[i]))
		}
		b.WriteString(leftPart)
		b.WriteString(strings.Repeat(" ", padding))
		b.WriteString(gutter)
		b.WriteString(strings.TrimRight(rightPart, " "))
		b.WriteString("\n")
	}
	fmt.Fprint(a.Out, b.String())
	return nil
}

// aboutRows collects the facts shown on the card. Anything that cannot be read
// degrades to a label rather than failing the command: `orbit about` must work
// on a fresh machine with no configuration.
func (a *App) aboutRows() []aboutRow {
	rows := []aboutRow{{label: a.Bundle.T("about.label.platform"), value: "Tencent Cloud"}}

	aliases := a.knownAliases()
	switch len(aliases) {
	case 0:
		rows = append(rows, aboutRow{a.Bundle.T("about.label.accounts"), a.Bundle.T("about.none")})
	default:
		rows = append(rows, aboutRow{
			label: a.Bundle.T("about.label.accounts"),
			value: fmt.Sprintf("%d (%s)", len(aliases), strings.Join(aliases, ", ")),
		})
	}

	products := []string{}
	if a.Loaded != nil {
		products = a.Loaded.Config.Defaults.Products
	}
	if len(products) == 0 {
		products = config.SupportedProducts
	}
	rows = append(rows, aboutRow{a.Bundle.T("about.label.products"), "ssl, " + strings.Join(products, ", ")})

	rows = append(rows,
		aboutRow{a.Bundle.T("about.label.locale"), a.Bundle.Lang()},
		aboutRow{a.Bundle.T("about.label.home"), shortenHome(config.Home())},
		aboutRow{a.Bundle.T("about.label.config"), shortenHome(config.GlobalConfigPath())},
		aboutRow{a.Bundle.T("about.label.credentials"), a.credentialsSummary()},
		aboutRow{a.Bundle.T("about.label.audit"), a.auditSummary()},
		aboutRow{a.Bundle.T("about.label.runtime"),
			fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)},
	)
	return rows
}

// credentialsSummary reports the credential file and its mode, because an
// over-permissive file is the one thing worth flagging on a summary screen.
func (a *App) credentialsSummary() string {
	path := config.CredentialsPath()
	info, err := os.Stat(path)
	if err != nil {
		return shortenHome(path) + "  " + a.Bundle.T("about.absent")
	}
	mode := info.Mode().Perm()
	summary := shortenHome(path) + fmt.Sprintf("  %04o", mode)
	if mode&0o077 != 0 {
		summary += "  " + a.Bundle.T("about.insecure")
	}
	return summary
}

// auditSummary reports where runs are recorded and how many there are.
func (a *App) auditSummary() string {
	path := filepath.Join(config.AuditDir(), "audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return shortenHome(path) + "  " + a.Bundle.T("about.no_runs")
	}
	runs := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" && strings.Contains(line, `"kind":"summary"`) {
			runs++
		}
	}
	return shortenHome(path) + "  " + a.Bundle.T("about.runs", "count", runs)
}

// shortenHome replaces the user's home directory with ~ for readable paths.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
