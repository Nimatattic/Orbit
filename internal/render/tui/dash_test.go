package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/i18n"
)

func testDashModel(t *testing.T, scan func(context.Context) (AccountView, error), checks func(context.Context) ([]core.Check, error)) *dashModel {
	t.Helper()
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newDashModel(ctx, cancel, bundle, DashJob{
		Account: "main", Products: []string{"cdn", "teo"}, Scan: scan, Checks: checks,
	}, true)
}

func loadedDash(t *testing.T, pending, candidates int) *dashModel {
	t.Helper()
	view := AccountView{Account: "main", Total: 5, Uploaded: 4, InUse: 2, Unused: 2, Resources: 3, ScannedAt: time.Now()}
	for i := 0; i < pending; i++ {
		view.Pending = append(view.Pending, NewItem("cdn", "p.example.com", "c-new", ""))
	}
	for i := 0; i < candidates; i++ {
		view.Candidates = append(view.Candidates, NewItem("ssl", "old", "c-new", "c-old"))
	}
	m := testDashModel(t, func(context.Context) (AccountView, error) { return view, nil }, nil)
	next, _ := m.Update(dashScanMsg{view: view})
	return next.(*dashModel)
}

func TestDashStartsWithScanInFlight(t *testing.T) {
	m := testDashModel(t, func(context.Context) (AccountView, error) { return AccountView{}, nil }, nil)
	if !m.loading {
		t.Fatal("the console should scan on open")
	}

	// Acting while the snapshot is missing must do nothing.
	next, cmd := m.Update(key("enter"))
	m = next.(*dashModel)
	if cmd != nil || m.outcome.Action != "" {
		t.Fatalf("actions must wait for the scan, got action=%q cmd=%v", m.outcome.Action, cmd)
	}
}

func TestDashMenuCursorWraps(t *testing.T) {
	m := loadedDash(t, 1, 0)

	next, _ := m.Update(key("up"))
	m = next.(*dashModel)
	if m.cursor != len(dashItemKeys)-1 {
		t.Fatalf("up from the first item must wrap to the last, got %d", m.cursor)
	}
	next, _ = m.Update(key("down"))
	m = next.(*dashModel)
	if m.cursor != 0 {
		t.Fatalf("down from the last item must wrap to the first, got %d", m.cursor)
	}
	next, _ = m.Update(key("down"))
	m = next.(*dashModel)
	if m.cursor != dashClean {
		t.Fatalf("cursor = %d, want the cleanup entry", m.cursor)
	}
}

func TestDashReturnsTheChosenFunction(t *testing.T) {
	m := loadedDash(t, 3, 1)

	next, cmd := m.Update(key("enter")) // first entry is apply
	m = next.(*dashModel)
	if cmd == nil {
		t.Fatal("choosing a function must quit the console")
	}
	if m.outcome.Action != "update" {
		t.Fatalf("action = %q, want update", m.outcome.Action)
	}

	next, _ = m.Update(key("2")) // number keys run entries directly
	m = next.(*dashModel)
	if m.outcome.Action != "clean" {
		t.Fatalf("action = %q, want clean", m.outcome.Action)
	}

	next, _ = m.Update(key("3"))
	m = next.(*dashModel)
	if m.outcome.Action != "status" {
		t.Fatalf("action = %q, want status", m.outcome.Action)
	}
}

func TestDashSelfTestStaysInConsole(t *testing.T) {
	calls := 0
	m := testDashModel(t,
		func(context.Context) (AccountView, error) { return AccountView{}, nil },
		func(context.Context) ([]core.Check, error) {
			calls++
			return []core.Check{{Key: "cloud.check.identity", Status: core.CheckOK, Detail: "AccountId=1"}}, nil
		})
	next, _ := m.Update(dashScanMsg{view: AccountView{Account: "main"}})
	m = next.(*dashModel)

	// `d` loads the self-test and shows it instead of leaving the console.
	next, cmd := m.Update(key("d"))
	m = next.(*dashModel)
	if cmd == nil || !m.showChecks || !m.checksLoading {
		t.Fatalf("d should start the self-test, show=%v loading=%v", m.showChecks, m.checksLoading)
	}
	if m.outcome.Action != "" {
		t.Fatal("the self-test must not leave the console")
	}
	next, _ = m.Update(dashChecksMsg{checks: []core.Check{{Key: "cloud.check.identity", Status: core.CheckOK}}})
	m = next.(*dashModel)
	if cmd == nil && calls == 0 {
		t.Fatal("the self-test callback should have been used")
	}
	if m.checksLoading || !m.checksLoaded || len(m.checks) != 1 {
		t.Fatalf("checks state = %+v", m.checks)
	}

	// Pressing d again only hides the panel: no second call.
	next, _ = m.Update(key("d"))
	m = next.(*dashModel)
	if m.showChecks {
		t.Fatal("d should toggle the panel off")
	}
	if calls > 1 {
		t.Fatalf("the self-test should be cached, got %d calls", calls)
	}
}

func TestDashQuitAndRescan(t *testing.T) {
	m := loadedDash(t, 1, 1)

	next, cmd := m.Update(key("q"))
	m = next.(*dashModel)
	if cmd == nil || m.outcome.Action != "" {
		t.Fatal("q must quit without choosing a function")
	}

	m = loadedDash(t, 1, 1)
	next, cmd = m.Update(key("r"))
	m = next.(*dashModel)
	if cmd == nil || !m.loading {
		t.Fatal("r must rescan the account")
	}
}

func TestDashShowsScanFailure(t *testing.T) {
	m := testDashModel(t, nil, nil)
	next, _ := m.Update(dashScanMsg{err: errors.New("账号 main 未登记凭证")})
	m = next.(*dashModel)
	out := m.View()
	if !strings.Contains(out, "Scan failed") {
		t.Fatalf("the failure must be visible:\n%s", out)
	}

	// A failed snapshot must not allow cloud-changing functions.
	next, cmd := m.Update(key("1"))
	m = next.(*dashModel)
	if cmd != nil || m.outcome.Action != "" {
		t.Fatalf("apply must be refused without a snapshot, got %q", m.outcome.Action)
	}
	// The overview is read-only, so it stays available.
	next, _ = m.Update(key("3"))
	m = next.(*dashModel)
	if m.outcome.Action != "status" {
		t.Fatalf("the overview should still open, got %q", m.outcome.Action)
	}
}

func TestDashViewRendersPanelsAndBadges(t *testing.T) {
	m := loadedDash(t, 4, 1)
	m.width, m.height = 96, 26
	out := m.View()
	for _, want := range []string{"console", "account main", "overview", "functions", "Apply certificates", "4 pending", "1 candidates", "enter"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view is missing %q:\n%s", want, out)
		}
	}
	if lines := strings.Split(out, "\n"); len(lines) != 26 {
		t.Fatalf("the console must fill the screen, got %d lines", len(lines))
	}
}

func TestPickerSingleModeConfirmsTheCursorRow(t *testing.T) {
	bundle, _ := i18n.New("en-US")
	m := newPickerModel(bundle, PickerJob{Title: "pick", Accounts: []string{"main", "hk"}, Single: true}, true)

	next, _ := m.Update(key("down"))
	m = next.(*pickerModel)
	next, cmd := m.Update(key("enter"))
	m = next.(*pickerModel)
	if !m.done || cmd == nil {
		t.Fatal("enter must confirm in single mode")
	}
	if sel := m.selected(); len(sel) != 1 || sel[0] != "hk" {
		t.Fatalf("single mode must pick exactly the cursor row, got %v", sel)
	}

	// Space also confirms, and never accumulates a second choice.
	next, _ = m.Update(key("up"))
	m = next.(*pickerModel)
	next, _ = m.Update(key(" "))
	m = next.(*pickerModel)
	if sel := m.selected(); len(sel) != 1 || sel[0] != "main" {
		t.Fatalf("space should replace the choice, got %v", sel)
	}
	if out := m.View(); strings.Contains(out, "all") {
		t.Fatalf("single mode must not advertise the bulk keys:\n%s", out)
	}
}
