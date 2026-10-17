package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Crequency/orbit/internal/i18n"
)

func testStatusModel(t *testing.T, accounts []string, scan func(context.Context, string) (AccountView, error)) *statusModel {
	t.Helper()
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newStatusModel(ctx, cancel, bundle, StatusJob{Title: "status", Accounts: accounts, Scan: scan}, true)
}

func TestStatusScansAccountsOneAtATime(t *testing.T) {
	var started []string
	m := testStatusModel(t, []string{"main", "hk"}, func(_ context.Context, account string) (AccountView, error) {
		started = append(started, account)
		return AccountView{Account: account, ScannedAt: time.Now()}, nil
	})

	// Init schedules the first account only; the rest follow one by one.
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must schedule the first scan")
	}
	if len(started) != 0 {
		t.Fatalf("scans run in commands, not synchronously: %v", started)
	}
	if !m.states["main"].loading || m.states["hk"].loading {
		t.Fatal("only one account should be in flight at a time")
	}

	next, cmd := m.Update(accountScannedMsg{account: "main", view: AccountView{Account: "main"}})
	m = next.(*statusModel)
	if !m.states["main"].loaded {
		t.Fatal("the scanned account must be marked loaded")
	}
	if cmd == nil {
		t.Fatal("the next unscanned account must be scheduled")
	}
	if !m.states["hk"].loading {
		t.Fatal("the second account should now be loading")
	}
}

func TestStatusTabSwitchingAndOutcome(t *testing.T) {
	m := testStatusModel(t, []string{"main", "hk", "third"}, nil)

	next, _ := m.Update(key("tab"))
	m = next.(*statusModel)
	if m.tab != 1 {
		t.Fatalf("tab = %d, want 1", m.tab)
	}
	next, _ = m.Update(key("3"))
	m = next.(*statusModel)
	if m.tab != 2 {
		t.Fatalf("number key should jump to tab 3, got %d", m.tab)
	}
	next, _ = m.Update(key("tab"))
	m = next.(*statusModel)
	if m.tab != 0 {
		t.Fatalf("tabs must wrap around, got %d", m.tab)
	}

	// `u` records the intent and quits so the CLI can start the run view.
	next, cmd := m.Update(key("u"))
	m = next.(*statusModel)
	if cmd == nil {
		t.Fatal("u must quit the overview")
	}
	if m.outcome.Action != "update" || m.outcome.Account != "main" {
		t.Fatalf("outcome = %+v, want update/main", m.outcome)
	}
}

func TestStatusDoesNotRescanFailedAccounts(t *testing.T) {
	m := testStatusModel(t, []string{"main", "hk"}, nil)

	next, _ := m.Update(accountScannedMsg{account: "main", err: errors.New("UnauthorizedOperation")})
	m = next.(*statusModel)
	// main failed, so the next account is scheduled exactly once...
	if !m.states["hk"].loading {
		t.Fatal("the next account should be scheduled")
	}
	next, cmd := m.Update(accountScannedMsg{account: "hk", err: errors.New("UnauthorizedOperation")})
	m = next.(*statusModel)
	// ...and after every account has been attempted, nothing is re-queued.
	if cmd != nil {
		t.Fatal("a failed account must not be re-queued automatically")
	}
	if !m.states["main"].attempted || !m.states["hk"].attempted {
		t.Fatal("both accounts should be marked attempted")
	}

	// The operator can still retry explicitly with r (the current tab is main).
	next, cmd = m.Update(key("r"))
	m = next.(*statusModel)
	if cmd == nil || !m.states["main"].loading {
		t.Fatalf("r must retry the current account, loading=%v cmd=%v", m.states["main"].loading, cmd != nil)
	}
}

func TestStatusRecordsScanErrors(t *testing.T) {
	m := testStatusModel(t, []string{"main"}, nil)
	next, _ := m.Update(accountScannedMsg{account: "main", err: errors.New("UnauthorizedOperation")})
	m = next.(*statusModel)
	st := m.states["main"]
	if st.err == nil || st.loaded {
		t.Fatalf("a failed scan must be recorded as an error: %+v", st)
	}
	if got := m.View(); got == "" {
		t.Fatal("the view must render the error state")
	}
}

func TestPickerTogglesAndRefusesEmptySelection(t *testing.T) {
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	m := newPickerModel(bundle, PickerJob{Title: "pick", Accounts: []string{"main", "hk"}}, true)

	next, _ := m.Update(key(" "))
	m = next.(*pickerModel)
	if len(m.selected()) != 1 || m.selected()[0] != "main" {
		t.Fatalf("space must toggle the cursor row, got %v", m.selected())
	}

	next, _ = m.Update(key("a")) // one of two selected, so this selects all
	m = next.(*pickerModel)
	if len(m.selected()) != 2 {
		t.Fatalf("'a' should select all when not everything is selected, got %v", m.selected())
	}

	next, _ = m.Update(key("a")) // everything is selected now, so this clears
	m = next.(*pickerModel)
	if len(m.selected()) != 0 {
		t.Fatalf("'a' should clear when all are selected, got %v", m.selected())
	}

	next, cmd := m.Update(key("enter"))
	m = next.(*pickerModel)
	if m.done || cmd != nil {
		t.Fatal("an empty selection must not be confirmable")
	}
	if m.errKey != "picker.none_selected" {
		t.Fatalf("expected an explanatory error, got %q", m.errKey)
	}

	next, _ = m.Update(key("a"))
	m = next.(*pickerModel)
	next, cmd = m.Update(key("enter"))
	m = next.(*pickerModel)
	if !m.done || cmd == nil {
		t.Fatal("selecting all then enter must confirm")
	}
	if len(m.selected()) != 2 {
		t.Fatalf("expected both accounts, got %v", m.selected())
	}
}

func TestPickerCancel(t *testing.T) {
	bundle, _ := i18n.New("en-US")
	m := newPickerModel(bundle, PickerJob{Accounts: []string{"main", "hk"}}, true)
	next, cmd := m.Update(key("esc"))
	m = next.(*pickerModel)
	if !m.cancel || cmd == nil {
		t.Fatal("esc must cancel the picker")
	}
}

func statusItemsModel(t *testing.T) *statusModel {
	t.Helper()
	m := testStatusModel(t, []string{"main"}, nil)
	next, _ := m.Update(accountScannedMsg{account: "main", view: AccountView{
		Account: "main",
		Pending: []Item{
			NewItem("cdn", "a.example.com", "c-new", ""),
			NewItem("cdn", "b.example.com", "c-new", ""),
			NewItem("teo", "c.example.com", "c-new", ""),
		},
		Candidates: []Item{
			NewItem("ssl", "old-cert", "c-new", "c-old"),
		},
	}})
	return next.(*statusModel)
}

func TestStatusCursorMovesWithinFocusedSection(t *testing.T) {
	m := statusItemsModel(t)

	next, _ := m.Update(key("down"))
	m = next.(*statusModel)
	if got := m.currentState().cursor[sectionPending]; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
	next, _ = m.Update(key("down"))
	next, _ = next.(*statusModel).Update(key("down")) // must clamp at the last row
	m = next.(*statusModel)
	if got := m.currentState().cursor[sectionPending]; got != 2 {
		t.Fatalf("cursor should clamp to the last row, got %d", got)
	}
	next, _ = m.Update(key("up"))
	m = next.(*statusModel)
	if got := m.currentState().cursor[sectionPending]; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}

	// Switching the section gives the candidate list its own cursor.
	next, _ = m.Update(key("s"))
	m = next.(*statusModel)
	if m.focus != sectionCandidates {
		t.Fatalf("focus = %d, want candidates", m.focus)
	}
	if got := m.currentState().cursor[sectionCandidates]; got != 0 {
		t.Fatalf("the candidate cursor starts at 0, got %d", got)
	}
}

func TestStatusSpaceTogglesAndBulkKeys(t *testing.T) {
	m := statusItemsModel(t)

	next, _ := m.Update(key(" "))
	m = next.(*statusModel)
	sel := m.currentState().selectedItems(sectionPending)
	if len(sel) != 1 || sel[0].Domain != "a.example.com" {
		t.Fatalf("space must select the row under the cursor, got %v", sel)
	}

	next, _ = m.Update(key("a"))
	m = next.(*statusModel)
	if got := len(m.currentState().selectedItems(sectionPending)); got != 3 {
		t.Fatalf("'a' must select all pending rows, got %d", got)
	}

	next, _ = m.Update(key("n"))
	m = next.(*statusModel)
	if got := len(m.currentState().selectedItems(sectionPending)); got != 0 {
		t.Fatalf("'n' must clear the selection, got %d", got)
	}

	// Selection is per section: selecting a candidate must not touch pending.
	next, _ = m.Update(key("s"))
	m = next.(*statusModel)
	next, _ = m.Update(key(" "))
	m = next.(*statusModel)
	if got := len(m.currentState().selectedItems(sectionCandidates)); got != 1 {
		t.Fatalf("candidate selection failed, got %d", got)
	}
	if got := len(m.currentState().selectedItems(sectionPending)); got != 0 {
		t.Fatalf("pending selection must stay empty, got %d", got)
	}
}

func TestStatusApplyCarriesTheSelection(t *testing.T) {
	m := statusItemsModel(t)

	// Select the first two pending rows.
	next, _ := m.Update(key(" "))
	m = next.(*statusModel)
	next, _ = m.Update(key("down"))
	m = next.(*statusModel)
	next, _ = m.Update(key(" "))
	m = next.(*statusModel)

	next, cmd := m.Update(key("u"))
	m = next.(*statusModel)
	if cmd == nil || m.outcome.Action != "update" || m.outcome.Account != "main" {
		t.Fatalf("outcome = %+v", m.outcome)
	}
	if len(m.outcome.Keys) != 2 {
		t.Fatalf("expected two selected keys, got %v", m.outcome.Keys)
	}
	for _, want := range []string{"cdn|a.example.com", "cdn|b.example.com"} {
		found := false
		for _, got := range m.outcome.Keys {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing key %q in %v", want, m.outcome.Keys)
		}
	}

	// Without a selection the outcome stays empty, which the CLI reads as "all".
	next, _ = m.Update(key("n"))
	m = next.(*statusModel)
	next, _ = m.Update(key("u"))
	m = next.(*statusModel)
	if len(m.outcome.Keys) != 0 {
		t.Fatalf("an empty selection must stay empty, got %v", m.outcome.Keys)
	}
}

func TestStatusCleanupCarriesSelectedCertIDs(t *testing.T) {
	m := statusItemsModel(t)
	next, _ := m.Update(key("s")) // focus the candidate list
	m = next.(*statusModel)
	next, _ = m.Update(key(" "))
	m = next.(*statusModel)

	next, cmd := m.Update(key("c"))
	m = next.(*statusModel)
	if cmd == nil || m.outcome.Action != "clean" {
		t.Fatalf("outcome = %+v", m.outcome)
	}
	if len(m.outcome.Certs) != 1 || m.outcome.Certs[0] != "c-old" {
		t.Fatalf("certs = %v, want [c-old]", m.outcome.Certs)
	}
}

func TestStatusViewRendersPanelsAndFooter(t *testing.T) {
	m := statusItemsModel(t)
	m.width, m.height = 90, 30
	out := m.View()
	for _, want := range []string{"Orbit · status", "▸", "to apply", "cleanup candidates", "space"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view is missing %q:\n%s", want, out)
		}
	}
	if lines := strings.Split(out, "\n"); len(lines) != 30 {
		t.Fatalf("view must fill the screen height, got %d lines", len(lines))
	}
}
