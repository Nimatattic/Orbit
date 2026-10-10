package tui

import (
	"context"
	"errors"
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
