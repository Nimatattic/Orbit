package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nimatattic/Orbit/internal/cloud"
	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/i18n"
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

// TestProgressMatchingPrefersTheProduct covers a hostname that both CDN and
// EdgeOne manage: an event carrying a product must only move that product's row,
// while an event without one keeps the legacy domain-only behaviour.
func TestProgressMatchingPrefersTheProduct(t *testing.T) {
	events := make(chan cloud.Progress, 8)
	m := testModel(t, Job{Title: "update"}, events)
	next, _ := m.Update(scanDoneMsg{items: []Item{
		NewItem("cdn", "www.example.com", "c-new", ""),
		NewItem("teo", "www.example.com", "c-new", ""),
	}})
	m = next.(*Model)
	next, _ = m.Update(key("y"))
	m = next.(*Model)

	feed := func(name string, kv ...any) {
		next, _ := m.Update(progressMsg{event: cloud.Progress{Key: name, Args: kv}})
		m = next.(*Model)
	}

	feed("progress.apply.verified", "product", "cdn", "domain", "www.example.com")
	if got := m.byKey["cdn|www.example.com"].state; got != stateVerified {
		t.Fatalf("the addressed row should be verified, got %v", got)
	}
	if got := m.byKey["teo|www.example.com"].state; got != stateSubmitting {
		t.Fatalf("the twin row of the other product must stay untouched, got %v", got)
	}

	// A product nobody has a row for matches nothing at all.
	feed("progress.apply.verified", "product", "ssl", "domain", "www.example.com")
	if got := m.byKey["teo|www.example.com"].state; got != stateSubmitting {
		t.Fatalf("an unknown product must not update rows, got %v", got)
	}

	// No product in the event: the domain alone decides, as before.
	feed("progress.apply.verifying", "domain", "www.example.com")
	for _, k := range []string{"cdn|www.example.com", "teo|www.example.com"} {
		if got := m.byKey[k].state; got != stateWaiting {
			t.Fatalf("%s should be waiting after a domain-only event, got %v", k, got)
		}
	}

	feed("progress.apply.failed", "product", "teo", "domain", "www.example.com", "code", "UnauthorizedOperation")
	row := m.byKey["teo|www.example.com"]
	if row.state != stateFailed || row.detail != "UnauthorizedOperation" {
		t.Fatalf("failure must carry its code, got state=%v detail=%q", row.state, row.detail)
	}
	if got := m.byKey["cdn|www.example.com"].state; got != stateWaiting {
		t.Fatalf("the CDN row must not inherit the EdgeOne failure, got %v", got)
	}
}

// TestPumpEventsIsReleasedOnExit guards the goroutine leak: the provider channel
// is never closed, so the pump must stop when the program leaves the terminal.
func TestPumpEventsIsReleasedOnExit(t *testing.T) {
	events := make(chan cloud.Progress) // unbuffered on purpose: the pump blocks
	m := testModel(t, Job{Title: "update"}, events)

	blocked := make(chan tea.Msg, 1)
	go func() { blocked <- m.pumpEvents()() }()

	m.stopPump()
	select {
	case msg := <-blocked:
		if _, ok := msg.(eventsClosedMsg); !ok {
			t.Fatalf("pump returned %T, want eventsClosedMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pumpEvents must not outlive the program")
	}

	// A pump started after the quit returns immediately instead of parking a new
	// goroutine on the channel.
	late := make(chan tea.Msg, 1)
	go func() { late <- m.pumpEvents()() }()
	select {
	case msg := <-late:
		if _, ok := msg.(eventsClosedMsg); !ok {
			t.Fatalf("late pump returned %T, want eventsClosedMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pump started after the quit must return immediately")
	}

	// The cancel path calls stopPump too, so it must be idempotent.
	m.stopPump()
}

func TestPumpEventsStopsWhenContextIsCancelled(t *testing.T) {
	events := make(chan cloud.Progress)
	m := testModel(t, Job{Title: "update"}, events)

	blocked := make(chan tea.Msg, 1)
	go func() { blocked <- m.pumpEvents()() }()

	m.cancel()
	select {
	case msg := <-blocked:
		if _, ok := msg.(eventsClosedMsg); !ok {
			t.Fatalf("pump returned %T, want eventsClosedMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the run must stop the pump")
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
