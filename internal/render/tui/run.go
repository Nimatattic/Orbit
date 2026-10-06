package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/i18n"
)

// Run takes over the terminal for one interactive run and restores it on exit.
//
// The provider writes its progress events into events (see App.progressSink), so
// the same instrumentation that drives the CLI log drives the TUI.
func Run(ctx context.Context, bundle *i18n.Bundle, events <-chan cloud.Progress, job Job) (Outcome, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	noColor := os.Getenv("NO_COLOR") != ""
	model := newModel(runCtx, cancel, bundle, events, job, noColor)

	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	)
	final, err := program.Run()
	if err != nil {
		return Outcome{}, err
	}

	done, ok := final.(*Model)
	if !ok {
		return Outcome{}, nil
	}
	return Outcome{
		Confirmed: done.confirmed,
		Cancelled: done.cancelled,
		Results:   done.results,
		Scanned:   done.jobItems(),
	}, done.scanErr
}

// jobItems exposes the scanned rows (for the caller's post-run summary).
func (m *Model) jobItems() []Item {
	out := make([]Item, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r.item)
	}
	return out
}
