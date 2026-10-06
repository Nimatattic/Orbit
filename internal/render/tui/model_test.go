package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/i18n"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func testModel(t *testing.T, job Job, events <-chan cloud.Progress) *Model {
	t.Helper()
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newModel(ctx, cancel, bundle, events, job, true)
}

func items() []Item {
	return []Item{
		NewItem("cdn", "www.example.com", "c-new", "c-old"),
		NewItem("teo", "api.example.com", "c-new", "teo-free"),
	}
}

func TestScanThenConfirmThenRun(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)

	if m.phase != phaseScanning {
		t.Fatalf("initial phase = %v, want scanning", m.phase)
	}

	// Scan finished: we move to the confirmation screen and every row is pending.
	next, _ := m.Update(scanDoneMsg{items: items()})
	m = next.(*Model)
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirm", m.phase)
	}
	if ok, unconfirmed, failed, pending := m.counts(); ok+unconfirmed+failed != 0 || pending != 2 {
		t.Fatalf("counts = ok:%d unconfirmed:%d failed:%d pending:%d, want 2 pending", ok, unconfirmed, failed, pending)
	}

	// Confirm: the run starts and rows become "submitting".
	next, cmd := m.Update(key("y"))
	m = next.(*Model)
	if m.phase != phaseRunning || cmd == nil {
		t.Fatalf("phase = %v cmd=%v, want running with a command", m.phase, cmd)
	}
	if _, _, _, active := m.counts(); active != 2 {
		t.Fatalf("rows are still in flight, so active should stay 2, got %d", active)
	}
	if m.pendingOnly() != 0 {
		t.Fatalf("rows must leave the pending state on confirm, got %d still pending", m.pendingOnly())
	}
}

func TestProgressEventsDriveRowStates(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)
	next, _ := m.Update(scanDoneMsg{items: items()})
	m = next.(*Model)
	next, _ = m.Update(key("y"))
	m = next.(*Model)

	feed := func(key string, kv ...any) {
		next, _ := m.Update(progressMsg{event: cloud.Progress{Key: key, Args: kv}})
		m = next.(*Model)
	}
	feed("progress.apply.item", "index", 1, "total", 2, "domain", "www.example.com", "cert", "c-new")
	feed("progress.apply.verifying", "domain", "www.example.com")
	if m.byKey["cdn|www.example.com"].state != stateWaiting {
		t.Fatalf("row should be waiting, got %v", m.byKey["cdn|www.example.com"].state)
	}
	feed("progress.apply.verified", "domain", "www.example.com")
	if m.byKey["cdn|www.example.com"].state != stateVerified {
		t.Fatalf("row should be verified, got %v", m.byKey["cdn|www.example.com"].state)
	}

	// Batching: one event can cover several domains (EdgeOne sends them at once).
	feed("progress.apply.verifying", "domain", "api.example.com, other.example.com")
	if m.byKey["teo|api.example.com"].state != stateWaiting {
		t.Fatalf("batched event must reach every listed domain, got %v", m.byKey["teo|api.example.com"].state)
	}

	feed("progress.apply.failed", "domain", "api.example.com", "code", "UnauthorizedOperation")
	row := m.byKey["teo|api.example.com"]
	if row.state != stateFailed || row.detail != "UnauthorizedOperation" {
		t.Fatalf("failure must carry its code, got state=%v detail=%q", row.state, row.detail)
	}
}

func TestCancelBeforeConfirmWritesNothing(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	applied := false
	m := testModel(t, Job{
		Title: "update",
		Apply: func(context.Context) ([]core.Result, error) { applied = true; return nil, nil },
	}, events)
	next, _ := m.Update(scanDoneMsg{items: items()})
	m = next.(*Model)

	next, cmd := m.Update(key("esc"))
	m = next.(*Model)
	if !m.cancelled || m.confirmed {
		t.Fatalf("esc must cancel without confirming: cancelled=%v confirmed=%v", m.cancelled, m.confirmed)
	}
	if cmd == nil {
		t.Fatal("cancelling should quit the program")
	}
	if applied {
		t.Fatal("the apply callback must not run when the user cancels")
	}
}

func TestNothingToWriteGoesStraightToDone(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)
	next, _ := m.Update(scanDoneMsg{items: nil})
	m = next.(*Model)
	if m.phase != phaseDone {
		t.Fatalf("phase = %v, want done when there is nothing to write", m.phase)
	}
}

func TestApplyResultsReconcileRows(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)
	next, _ := m.Update(scanDoneMsg{items: items()})
	m = next.(*Model)
	next, _ = m.Update(key("y"))
	m = next.(*Model)

	next, _ = m.Update(applyDoneMsg{results: []core.Result{
		{Change: core.Change{Product: core.ProductCDN, Domain: "www.example.com"}, OK: true, Verified: true},
		{Change: core.Change{Product: core.ProductTEO, Domain: "api.example.com"}, OK: true, Verified: false, ErrCode: "Timeout"},
	}})
	m = next.(*Model)
	if m.phase != phaseDone {
		t.Fatalf("phase = %v, want done", m.phase)
	}
	ok, unconfirmed, failed, _ := m.counts()
	if ok != 1 || unconfirmed != 1 || failed != 0 {
		t.Fatalf("counts = ok:%d unconfirmed:%d failed:%d, want 1/1/0", ok, unconfirmed, failed)
	}
	if m.byKey["teo|api.example.com"].detail != "Timeout" {
		t.Fatalf("unconfirmed row should keep its reason, got %q", m.byKey["teo|api.example.com"].detail)
	}
}

func TestWaitingLabelShowsElapsedTime(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)
	m.rows = []*row{{item: NewItem("cdn", "a.example.com", "c", ""), state: stateWaiting, since: time.Now().Add(-3 * time.Second)}}
	label := m.stateLabel(m.rows[0])
	if label == "" {
		t.Fatal("waiting rows must render a label")
	}
	if !strings.Contains(label, "3s") && !strings.Contains(label, "2s") {
		t.Fatalf("waiting label should show elapsed time, got %q", label)
	}
}
