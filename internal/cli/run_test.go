package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/config"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/credential"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/i18n"
)

// stubProvider lets the CLI be exercised without configuration files, real
// credentials or network access.
type stubProvider struct {
	scan   cloud.ScanResult
	apply  []core.Result
	delete []core.Result
}

func (s *stubProvider) Name() string                     { return "tencent" }
func (s *stubProvider) Capabilities() cloud.Capabilities { return cloud.Capabilities{} }
func (s *stubProvider) Preflight(context.Context, string) ([]core.Check, error) {
	return nil, nil
}
func (s *stubProvider) Scan(context.Context, string, cloud.ScanScope) (cloud.ScanResult, error) {
	return s.scan, nil
}
func (s *stubProvider) ListCertificates(context.Context, string, cloud.ScanScope) ([]core.Certificate, error) {
	return s.scan.Certificates, nil
}
func (s *stubProvider) ListResources(context.Context, string, cloud.ScanScope) ([]core.Resource, error) {
	return s.scan.Resources, nil
}
func (s *stubProvider) ListBindings(context.Context, string, []string) (map[string]core.BindingEvidence, error) {
	return s.scan.Evidence, nil
}
func (s *stubProvider) Apply(context.Context, string, core.Plan, cloud.ApplyOptions) ([]core.Result, error) {
	return s.apply, nil
}
func (s *stubProvider) DeleteCertificates(context.Context, string, []string, cloud.DeleteOptions) ([]core.Result, error) {
	return s.delete, nil
}
func (s *stubProvider) UploadCertificate(context.Context, string, cloud.LocalCert) (string, error) {
	return "", cloud.ErrNotImplemented
}

func timeAgo(h int) time.Time   { return time.Now().Add(-time.Duration(h) * time.Hour) }
func timeAhead(h int) time.Time { return time.Now().Add(time.Duration(h) * time.Hour) }

func testApp(t *testing.T, provider cloud.Provider) *App {
	t.Helper()
	bundle, err := i18n.New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cfg := &config.Config{
		Version: 1,
		Defaults: config.Defaults{
			Products: []string{"cdn", "teo"},
			Clean:    config.Clean{OlderThan: "7d", RequireFullEvidence: true},
			EdgeOne:  config.EdgeOne{ReplaceFreeCert: true},
		},
		Platforms: map[string]config.Platform{
			DefaultPlatform: {Accounts: []config.Account{{Alias: "main"}}},
		},
	}
	app := &App{
		Accounts:        "main",
		Loaded:          &config.Loaded{Config: cfg},
		Bundle:          bundle,
		Creds:           &credential.Credentials{Accounts: map[string]credential.Account{"main": {SecretID: "id", SecretKey: "key"}}},
		Out:             out,
		Err:             errOut,
		In:              bytes.NewReader(nil),
		WorkDir:         t.TempDir(),
		Home:            t.TempDir(),
		BackupDir:       t.TempDir(),
		providerFactory: func(string) (cloud.Provider, error) { return provider, nil },
	}
	app.testOut, app.testErr = out, errOut
	return app
}

func TestCleanRefusesOnIncompleteScan(t *testing.T) {
	provider := &stubProvider{scan: cloud.ScanResult{
		Certificates: []core.Certificate{{ID: "c-1", From: "upload", Domain: "a.example.com"}},
		Warnings: []cloud.ScanWarning{{
			Product: "teo", Action: "teo:DescribeZones", Code: "UnauthorizedOperation",
			MessageKey: "cloud.err.no_permission",
		}},
	}}
	app := testApp(t, provider)

	err := app.runClean(context.Background())
	var e *exitcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("a partial scan must block cleanup, got %v", err)
	}
	if e.Code != exitcode.GateRefused || e.Key != "scan.refused" {
		t.Fatalf("got code=%d key=%q, want %d/scan.refused", e.Code, e.Key, exitcode.GateRefused)
	}
	if !bytes.Contains(app.testErr.Bytes(), []byte("teo:DescribeZones")) {
		t.Fatalf("the warning must be printed, got %q", app.testErr.String())
	}
}

func TestCleanProceedsWhenPartialScanIsExplicitlyAllowed(t *testing.T) {
	provider := &stubProvider{scan: cloud.ScanResult{
		Warnings: []cloud.ScanWarning{{Product: "teo", Action: "teo:DescribeZones", Code: "UnauthorizedOperation"}},
	}}
	app := testApp(t, provider)
	app.PartialScan = true

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("--permit-partial-scan must let the run continue, got %v", err)
	}
}

func TestCleanRefusesWhenEvidenceQueryIsMissing(t *testing.T) {
	// No warnings, but the association evidence never arrived: gate G2 keeps
	// every certificate because it cannot prove they are unused.
	provider := &stubProvider{scan: cloud.ScanResult{
		Certificates: []core.Certificate{{
			ID: "c-old", From: "upload", Domain: "a.example.com",
			NotBefore: timeAgo(48), NotAfter: timeAhead(24),
		}},
	}}
	app := testApp(t, provider)

	if err := app.runClean(context.Background()); err != nil {
		t.Fatalf("clean must complete with no candidates, got %v", err)
	}
	if bytes.Contains(app.testOut.Bytes(), []byte("c-old")) &&
		bytes.Contains(app.testOut.Bytes(), []byte("候选删除")) {
		t.Fatalf("a certificate without evidence must not be a candidate: %s", app.testOut.String())
	}
}
