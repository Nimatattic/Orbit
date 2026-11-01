package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Nimatattic/Orbit/internal/cloud"
	"github.com/Nimatattic/Orbit/internal/config"
	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/credential"
	"github.com/Nimatattic/Orbit/internal/exitcode"
)

// recordingProvider wraps stubProvider to observe the write calls the CLI makes
// and the options it passes. That is what the dry-run contract is about: plan
// mode must not call the provider at all, and the DryRun option must still
// travel with every write request as a second line of defence.
type recordingProvider struct {
	*stubProvider

	checks      []core.Check
	applyCalls  int
	deleteCalls int
	lastApply   cloud.ApplyOptions
	lastDelete  cloud.DeleteOptions
}

func (p *recordingProvider) Preflight(context.Context, string) ([]core.Check, error) {
	return p.checks, nil
}

func (p *recordingProvider) Apply(ctx context.Context, account string, plan core.Plan, opts cloud.ApplyOptions) ([]core.Result, error) {
	p.applyCalls++
	p.lastApply = opts
	return p.stubProvider.Apply(ctx, account, plan, opts)
}

func (p *recordingProvider) DeleteCertificates(ctx context.Context, account string, ids []string, opts cloud.DeleteOptions) ([]core.Result, error) {
	p.deleteCalls++
	p.lastDelete = opts
	return p.stubProvider.DeleteCertificates(ctx, account, ids, opts)
}

// pendingUpdateScan is one CDN domain still pointing at an expired certificate
// while a better one is available: exactly one planned write.
func pendingUpdateScan(now time.Time) cloud.ScanResult {
	certs := []core.Certificate{
		{
			ID: "c-old", Alias: "old", From: core.SourceUpload, Domain: "example.com",
			SANs:      []string{"example.com", "www.example.com"},
			NotBefore: now.Add(-400 * 24 * time.Hour), NotAfter: now.Add(-10 * 24 * time.Hour),
			Status: 1, Deployable: true,
		},
		{
			ID: "c-new", Alias: "new", From: core.SourceUpload, Domain: "example.com",
			SANs:      []string{"example.com", "www.example.com"},
			NotBefore: now.Add(-10 * 24 * time.Hour), NotAfter: now.Add(300 * 24 * time.Hour),
			Status: 1, Deployable: true,
		},
	}
	resources := []core.Resource{{
		Account: "main", Product: core.ProductCDN, ID: "www.example.com", Name: "www.example.com",
		Domain: "www.example.com", BoundCert: "c-old", BoundNotAfter: certs[0].NotAfter,
	}}
	return cloud.ScanResult{Certificates: certs, Resources: resources}
}

// cleanScan is one superseded, unreferenced upload certificate with complete
// evidence: exactly one cleanup candidate.
func cleanScan(now time.Time) cloud.ScanResult {
	upload := func(id string, age, ttl time.Duration) core.Certificate {
		return core.Certificate{
			ID: id, Alias: id, From: core.SourceUpload, Domain: "a.example.com",
			SANs: []string{"a.example.com"}, NotBefore: now.Add(-age), NotAfter: now.Add(ttl),
			InsertTime: now.Add(-age), Status: 1, Deployable: true,
		}
	}
	certs := []core.Certificate{
		upload("c-old", 60*24*time.Hour, 10*24*time.Hour),
		upload("c-new", 5*24*time.Hour, 400*24*time.Hour),
	}
	evidence := map[string]core.BindingEvidence{}
	for _, c := range certs {
		evidence[c.ID] = core.BindingEvidence{CertID: c.ID, Status: 1, Complete: true, QueriedAt: now}
	}
	return cloud.ScanResult{Certificates: certs, Evidence: evidence}
}

// --- --fail-on-changes ----------------------------------------------------

func TestUpdateFailOnChangesIsSilentForAnEmptyPlan(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: cloud.ScanResult{}}}
	app := testApp(t, provider)
	app.FailOnChg = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("an empty plan must exit 0 with --fail-on-changes, got %v", err)
	}
	out := app.testOut.String() + app.testErr.String()
	if strings.Contains(out, app.Bundle.T("msg.fail_on_changes")) {
		t.Fatalf("an empty plan must not announce pending changes: %q", out)
	}
	if !strings.Contains(app.testOut.String(), app.Bundle.T("msg.nothing_to_do")) {
		t.Fatalf("an empty plan should say there is nothing to do: %q", app.testOut.String())
	}
	if provider.applyCalls != 0 {
		t.Fatal("an empty plan must not write")
	}
}

func TestUpdateFailOnChangesExitsThreeForPendingWork(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(time.Now())}}
	app := testApp(t, provider)
	app.FailOnChg = true

	err := app.runUpdate(context.Background())
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.PendingChanges {
		t.Fatalf("pending work with --fail-on-changes must exit 3, got %v", err)
	}
	if provider.applyCalls != 0 {
		t.Fatal("--fail-on-changes is a plan mode query; it must not write")
	}
	if strings.Contains(app.testOut.String(), app.Bundle.T("msg.nothing_to_do")) {
		t.Fatalf("a plan with work must not claim there is nothing to do: %q", app.testOut.String())
	}
}

func TestUpdateFailOnChangesAccumulatesAcrossAccounts(t *testing.T) {
	now := time.Now()
	idle := &recordingProvider{stubProvider: &stubProvider{scan: cloud.ScanResult{}}}
	busy := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(now)}}

	app := testApp(t, busy)
	app.FailOnChg = true
	app.Accounts = "idle,busy"
	app.Creds = &credential.Credentials{Accounts: map[string]credential.Account{}}
	app.Loaded.Config.Platforms[DefaultPlatform] = config.Platform{Accounts: []config.Account{
		{Alias: "idle"}, {Alias: "busy"},
	}}
	app.providerFactory = func(alias string) (cloud.Provider, error) {
		if alias == "idle" {
			return idle, nil
		}
		return busy, nil
	}

	err := app.runUpdate(context.Background())
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.PendingChanges {
		t.Fatalf("one account with work is enough to exit 3, got %v", err)
	}
	if idle.applyCalls != 0 || busy.applyCalls != 0 {
		t.Fatal("plan mode must never write, whichever account is scanned")
	}
}

func TestUpdateFailOnChangesIsSuppressedByYes(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(time.Now())}}
	app := testApp(t, provider)
	app.FailOnChg = true
	app.Yes = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("--yes already applied the changes; --fail-on-changes must not fire, got %v", err)
	}
	if provider.applyCalls != 1 {
		t.Fatalf("--yes must write, got %d apply call(s)", provider.applyCalls)
	}
}

func TestFailOnChangesKeepsTheCLIOnATerminal(t *testing.T) {
	// The exit code is the answer of the query, so an interactive TUI would hide
	// it: --fail-on-changes always stays in batch/plan mode.
	app := &App{Interactive: true, FailOnChg: true}
	if app.wantsTUI() {
		t.Fatal("--fail-on-changes must not hand over to the TUI")
	}
}

// --- --dry-run must win over --yes ---------------------------------------

func TestUpdateDryRunWinsOverYes(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(time.Now())}}
	app := testApp(t, provider)
	app.DryRun = true
	app.Yes = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if provider.applyCalls != 0 {
		t.Fatal("--dry-run --yes must not call Apply")
	}
	if !strings.Contains(app.testOut.String(), app.Bundle.T("msg.dry_run")) {
		t.Fatalf("a dry run should say nothing was written: %q", app.testOut.String())
	}
}

func TestUpdateYesWritesAndClearsDryRun(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(time.Now())}}
	app := testApp(t, provider)
	app.Yes = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("update --yes: %v", err)
	}
	if provider.applyCalls != 1 {
		t.Fatalf("--yes must call Apply once, got %d", provider.applyCalls)
	}
	if provider.lastApply.DryRun {
		t.Fatal("--yes must not ask the provider for a dry run")
	}
}

func TestCleanDryRunWinsOverYes(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: cleanScan(time.Now())}}
	app := testApp(t, provider)
	app.DryRun = true
	app.Yes = true

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean dry run: %v", err)
	}
	if provider.deleteCalls != 0 {
		t.Fatal("--dry-run --yes must not call DeleteCertificates")
	}
	if !strings.Contains(app.testOut.String(), app.Bundle.T("msg.dry_run")) {
		t.Fatalf("a dry run should say nothing was written: %q", app.testOut.String())
	}
	// A dry run has no local side effects either: no backup directory.
	entries, err := os.ReadDir(app.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a dry run must not write a backup, found %v", entries)
	}
}

func TestCleanYesBacksUpThenDeletes(t *testing.T) {
	provider := &recordingProvider{stubProvider: &stubProvider{scan: cleanScan(time.Now())}}
	app := testApp(t, provider)
	app.Yes = true

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean --yes: %v", err)
	}
	if provider.deleteCalls != 1 {
		t.Fatalf("--yes must call DeleteCertificates once, got %d", provider.deleteCalls)
	}
	if provider.lastDelete.DryRun {
		t.Fatal("--yes must not ask the provider for a dry run")
	}
	if !provider.lastDelete.CheckBindings {
		t.Fatal("deletion must always ask the provider to check bindings (decision T5)")
	}
	if _, err := os.Stat(filepath.Join(app.BackupDir, "main")); err != nil {
		t.Fatalf("the backup must be written before deleting: %v", err)
	}
}

func TestWriteOptionsAlwaysCarryDryRun(t *testing.T) {
	// The CLI short-circuits in plan mode, but the option must still be set:
	// a provider may not depend on its caller being careful.
	app := testApp(t, &stubProvider{})
	app.DryRun = true
	if opts := app.applyOptions(); !opts.DryRun {
		t.Fatal("cloud.ApplyOptions.DryRun must follow --dry-run")
	}
	if opts := app.deleteOptions(t.TempDir()); !opts.DryRun {
		t.Fatal("cloud.DeleteOptions.DryRun must follow --dry-run")
	}
	app.DryRun = false
	if opts := app.applyOptions(); opts.DryRun {
		t.Fatal("without --dry-run the provider must be allowed to write")
	}
}

// --- --ssl stays accepted but hidden -------------------------------------

func TestSSLIsAHiddenCompatibilityFlag(t *testing.T) {
	app := testApp(t, &stubProvider{})
	for name, build := range map[string]func() *cobra.Command{
		"update": app.newUpdateCmd,
		"clean":  app.newCleanCmd,
	} {
		cmd := build()
		flag := cmd.Flags().Lookup("ssl")
		if flag == nil {
			t.Fatalf("%s: --ssl must stay registered so old scripts do not fail on an unknown flag", name)
		}
		if !flag.Hidden {
			t.Fatalf("%s: --ssl is a compatibility no-op and must be hidden from help", name)
		}
		if err := cmd.Flags().Parse([]string{"--ssl"}); err != nil {
			t.Fatalf("%s: --ssl must still parse: %v", name, err)
		}
		if app.DryRun || app.Yes || app.FailOnChg {
			t.Fatalf("%s: --ssl must not change behaviour, got %+v", name, app)
		}
	}
}

// --- the dead --secret-id-env flag is gone --------------------------------

func TestAccountAddHasNoDeadSecretIDEnvFlag(t *testing.T) {
	app := testApp(t, &stubProvider{})
	cmd := app.newAccountAddCmd()
	if cmd.Flags().Lookup("secret-id-env") != nil {
		t.Fatal("--secret-id-env was dead weight (it only returned an error) and must be gone")
	}
	for _, want := range []string{"alias", "from-stdin", "force"} {
		if cmd.Flags().Lookup(want) == nil {
			t.Fatalf("--%s must stay registered", want)
		}
	}
}

// --- account list / account test --json -----------------------------------

func TestAccountListJSON(t *testing.T) {
	app := testApp(t, &stubProvider{})
	app.Creds = &credential.Credentials{Accounts: map[string]credential.Account{
		"main": {SecretID: "AKIDEXAMPLE4567", SecretKey: "secret"},
	}}
	app.JSON = true

	cmd := app.newAccountListCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("account list --json: %v", err)
	}
	got := app.testOut.String()
	if !strings.HasPrefix(got, "[\n  {") {
		t.Fatalf("JSON output should be indented by two spaces, got %q", got)
	}
	var dtos []accountDTO
	if err := json.Unmarshal([]byte(got), &dtos); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, got)
	}
	if len(dtos) != 1 || dtos[0].Account != "main" || dtos[0].Source != "store" {
		t.Fatalf("unexpected document: %+v", dtos)
	}
	if dtos[0].SecretID != credential.Redact("AKIDEXAMPLE4567") {
		t.Fatalf("the secret id must stay redacted in JSON, got %q", dtos[0].SecretID)
	}
	if strings.Contains(got, "AKIDEXAMPLE4567") || strings.Contains(got, "secret_key") {
		t.Fatalf("JSON must never carry a usable credential: %s", got)
	}
}

func TestAccountListJSONWithNoAccountsIsAnEmptyArray(t *testing.T) {
	app := testApp(t, &stubProvider{})
	app.Loaded.Config.Platforms = map[string]config.Platform{}
	app.Creds = &credential.Credentials{Accounts: map[string]credential.Account{}}
	app.JSON = true

	cmd := app.newAccountListCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("account list --json: %v", err)
	}
	if got := strings.TrimSpace(app.testOut.String()); got != "[]" {
		t.Fatalf("an empty registry must marshal as [] (not null or prose), got %q", got)
	}
}

func TestAccountTestJSON(t *testing.T) {
	provider := &recordingProvider{
		stubProvider: &stubProvider{},
		checks: []core.Check{
			{Key: "cloud.check.identity", Status: core.CheckOK, Detail: "AccountId 100000000001"},
			{Key: "cloud.check.cdn", Status: core.CheckFail, Detail: "denied", MessageKey: "cloud.err.no_permission"},
			{Key: "cloud.check.service_role", Status: core.CheckWarn},
		},
	}
	app := testApp(t, provider)
	app.JSON = true

	cmd := app.newAccountTestCmd()
	err := cmd.RunE(cmd, nil)
	if code := exitcode.CodeOf(err); code != exitcode.Partial {
		t.Fatalf("account test --json must keep the exit-code contract (2 on a failed check), got %d", code)
	}
	got := app.testOut.String()
	var docs []struct {
		Account string     `json:"account"`
		Checks  []checkDTO `json:"checks"`
	}
	if err := json.Unmarshal([]byte(got), &docs); err != nil {
		t.Fatalf("stdout must be valid JSON only: %v\n%s", err, got)
	}
	if len(docs) != 1 || docs[0].Account != "main" {
		t.Fatalf("unexpected document: %+v", docs)
	}
	if len(docs[0].Checks) != 3 {
		t.Fatalf("expected three checks, got %+v", docs[0].Checks)
	}
	if docs[0].Checks[1].Status != string(core.CheckFail) {
		t.Fatalf("status values must stay stable in JSON, got %+v", docs[0].Checks[1])
	}
	if docs[0].Checks[1].MessageKey != "cloud.err.no_permission" {
		t.Fatalf("JSON must carry the message key, not a translation, got %+v", docs[0].Checks[1])
	}
	if strings.Contains(got, app.Bundle.T("cloud.err.no_permission")) {
		t.Fatal("--json must stay language independent (decision B5)")
	}
}

// --- doctor ---------------------------------------------------------------

func TestDoctorExitsOneOnFailedCheck(t *testing.T) {
	app := testApp(t, &stubProvider{})
	path := filepath.Join(app.Home, "credentials.yaml")
	if err := os.WriteFile(path, []byte("accounts: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A credential file readable by others is a failed check.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := app.newDoctorCmd()
	err := cmd.RunE(cmd, nil)
	if code := exitcode.CodeOf(err); code != exitcode.Fatal {
		t.Fatalf("a failed doctor check must exit 1 (fatal), got %d (%v)", code, err)
	}
	out := app.testOut.String()
	if !strings.Contains(out, app.Bundle.T("doctor.title")) {
		t.Fatalf("doctor must still print every check, got %q", out)
	}
	if !strings.Contains(out, app.Bundle.T("doctor.fail")) {
		t.Fatalf("the failing check should be marked fail, got %q", out)
	}
}

func TestDoctorExitsZeroWithOnlyWarnings(t *testing.T) {
	app := testApp(t, &stubProvider{})
	app.Home = t.TempDir() // no credential file: a warning, not a failure

	cmd := app.newDoctorCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("warnings are acceptable and must exit 0, got %v", err)
	}
}

// --- clean --products -----------------------------------------------------

func TestCleanValidatesProductsLikeUpdate(t *testing.T) {
	app := testApp(t, &stubProvider{})
	if app.newCleanCmd().Flags().Lookup("products") == nil {
		t.Fatal("clean must accept --products like update/status")
	}

	app.Products = "bogus"
	err := app.runClean(context.Background())
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Fatal || e.Key != "err.invalid_products" {
		t.Fatalf("an unknown product must be refused before any API call, got %v", err)
	}

	provider := &stubProvider{}
	app = testApp(t, provider)
	app.Products = "cdn"
	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean --products=cdn: %v", err)
	}
	if len(provider.lastScope.Products) != 1 || provider.lastScope.Products[0] != "cdn" {
		t.Fatalf("--products must narrow the scan, got %v", provider.lastScope.Products)
	}
}

// --- warnings in the update/clean JSON documents --------------------------

func TestUpdateJSONCarriesScanWarnings(t *testing.T) {
	provider := &stubProvider{scan: cloud.ScanResult{Warnings: []cloud.ScanWarning{{
		Product: "teo", Action: "teo:DescribeZones", Code: "UnauthorizedOperation",
		MessageKey: "cloud.err.no_permission", Scope: "zone-a",
	}}}}
	app := testApp(t, provider)
	app.JSON = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("update --json: %v", err)
	}
	var doc updateOutput
	if err := json.Unmarshal(app.testOut.Bytes(), &doc); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("the plan document must carry the scan warnings, got %+v", doc)
	}
	w := doc.Warnings[0]
	if w.Product != "teo" || w.Code != "UnauthorizedOperation" || w.Scope != "zone-a" || !w.Blocking {
		t.Fatalf("unexpected warning document: %+v", w)
	}
	if w.Message != app.Bundle.T("cloud.err.no_permission") {
		t.Fatalf("the warning should carry its translated hint: %+v", w)
	}
}

func TestCleanJSONCarriesScanWarnings(t *testing.T) {
	provider := &stubProvider{scan: cloud.ScanResult{Warnings: []cloud.ScanWarning{{
		Product: "teo", Action: "teo:DescribeAccelerationDomains", Code: "ZoneNotApplicable",
		Scope: "zone-a", Informational: true,
	}}}}
	app := testApp(t, provider)
	app.JSON = true

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean --json: %v", err)
	}
	var doc cleanOutput
	if err := json.Unmarshal(app.testOut.Bytes(), &doc); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("the clean document must carry the scan warnings, got %+v", doc)
	}
	if doc.Warnings[0].Blocking {
		t.Fatalf("an informational warning must not be reported as blocking: %+v", doc.Warnings[0])
	}
}

func TestWriteJSONStdoutIsPureJSON(t *testing.T) {
	// update --yes --json emits the plan document followed by the results
	// document. A human summary line between them would break every consumer.
	provider := &recordingProvider{stubProvider: &stubProvider{scan: pendingUpdateScan(time.Now())}}
	app := testApp(t, provider)
	app.JSON = true
	app.Yes = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("update --yes --json: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(app.testOut.Bytes()))
	docs := 0
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stdout must be a concatenation of JSON documents: %v\n%s", err, app.testOut.String())
		}
		docs++
	}
	if docs != 2 {
		t.Fatalf("expected a plan document and a results document, got %d: %s", docs, app.testOut.String())
	}
}

func TestJSONOmitsWarningsWhenTheScanIsComplete(t *testing.T) {
	for _, run := range []func(*App) error{
		func(a *App) error { return a.runUpdate(context.Background()) },
		func(a *App) error { return a.runClean(context.Background()) },
	} {
		app := testApp(t, &stubProvider{})
		app.JSON = true
		if err := run(app); err != nil {
			t.Fatalf("run: %v", err)
		}
		if strings.Contains(app.testOut.String(), "warnings") {
			t.Fatalf("warnings are omitempty, like status: %s", app.testOut.String())
		}
	}
}

// --- snake_case wire contract for update/clean ----------------------------

// decodeJSONDocs reads every concatenated JSON document written to stdout.
func decodeJSONDocs(t *testing.T, raw []byte) []any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var docs []any
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}
		if err != nil {
			t.Fatalf("stdout must be JSON documents only: %v\n%s", err, raw)
		}
		docs = append(docs, doc)
	}
}

// uppercaseKeys reports every JSON key that leaks a Go field name (any
// uppercase letter): the wire contract is snake_case, the core types are not.
func uppercaseKeys(v any, path string) []string {
	var bad []string
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if strings.ToLower(key) != key {
				bad = append(bad, path+"."+key)
			}
			bad = append(bad, uppercaseKeys(child, path+"."+key)...)
		}
	case []any:
		for i, child := range node {
			bad = append(bad, uppercaseKeys(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return bad
}

func TestUpdateJSONUsesSnakeCaseFields(t *testing.T) {
	provider := &stubProvider{scan: pendingUpdateScan(time.Now())}
	app := testApp(t, provider)
	app.JSON = true

	if err := app.runUpdate(context.Background()); err != nil {
		t.Fatalf("update --json: %v", err)
	}
	docs := decodeJSONDocs(t, app.testOut.Bytes())
	if len(docs) != 1 {
		t.Fatalf("expected one plan document, got %d", len(docs))
	}
	if bad := uppercaseKeys(docs[0], "$"); len(bad) > 0 {
		t.Fatalf("JSON keys must be snake_case, Go field names leaked: %v", bad)
	}
	plan, ok := docs[0].(map[string]any)["plan"].(map[string]any)
	if !ok {
		t.Fatalf("document should carry the plan: %v", docs[0])
	}
	changes, _ := plan["changes"].([]any)
	if len(changes) == 0 {
		t.Fatalf("fixture should produce a change: %v", plan)
	}
	change, ok := changes[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected change document: %v", changes[0])
	}
	for _, key := range []string{"product", "domain", "kind", "strategy", "risk", "resource_id", "reasons"} {
		if _, ok := change[key]; !ok {
			t.Errorf("plan change is missing %q: %v", key, change)
		}
	}
	to, ok := change["to"].(map[string]any)
	if !ok {
		t.Fatalf("plan change should carry a target certificate: %v", change)
	}
	for _, key := range []string{"cert_id", "alias", "source", "not_after"} {
		if _, ok := to[key]; !ok {
			t.Errorf("certificate reference is missing %q: %v", key, to)
		}
	}
}

func TestCleanJSONUsesSnakeCaseFields(t *testing.T) {
	provider := &stubProvider{scan: cleanScan(time.Now())}
	app := testApp(t, provider)
	app.JSON = true

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean --json: %v", err)
	}
	docs := decodeJSONDocs(t, app.testOut.Bytes())
	if len(docs) != 1 {
		t.Fatalf("expected one cleanup document, got %d", len(docs))
	}
	if bad := uppercaseKeys(docs[0], "$"); len(bad) > 0 {
		t.Fatalf("JSON keys must be snake_case, Go field names leaked: %v", bad)
	}
	clean, ok := docs[0].(map[string]any)["clean"].(map[string]any)
	if !ok {
		t.Fatalf("document should carry the cleanup plan: %v", docs[0])
	}
	candidates, _ := clean["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("fixture should produce a candidate: %v", clean)
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected candidate document: %v", candidates[0])
	}
	cert, ok := candidate["cert"].(map[string]any)
	if !ok {
		t.Fatalf("candidate should carry its certificate: %v", candidate)
	}
	for _, key := range []string{"cert_id", "alias", "source", "not_after"} {
		if _, ok := cert[key]; !ok {
			t.Errorf("certificate document is missing %q: %v", key, cert)
		}
	}
	if _, ok := candidate["superseded_by"]; !ok {
		t.Errorf("candidate is missing superseded_by: %v", candidate)
	}
	if _, ok := candidate["evidence"]; !ok {
		t.Errorf("candidate is missing evidence: %v", candidate)
	}
}
