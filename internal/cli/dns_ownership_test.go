package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/exitcode"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

// notOwned is the exact error DNSPod returns for a zone that belongs to another
// account. It is not a CAM failure, so the CLI must treat it as a skip.
func notOwned() error {
	return tcerr.NewTencentCloudSDKError("OperationDenied.NoPermissionToOperateDomain",
		"当前域名无权限，请返回域名列表。", "req-test")
}

func multiAccountDNS(t *testing.T, listErr map[string]error) *App {
	t.Helper()
	provider := &stubProvider{
		dnsZones: []core.DNSZone{{ID: 1, Name: "example.com"}},
		dnsRecords: map[string][]core.DNSRecord{
			"example.com": {{ID: 1, Zone: "example.com", Name: "@", Type: "A", Value: "203.0.113.10", TTL: 600, Enabled: true}},
		},
		dnsListErr: listErr,
	}
	app := testApp(t, provider)
	app.Accounts = "main,hk"
	return app
}

func TestDNSListSkipsAccountsThatDoNotHostTheZone(t *testing.T) {
	app := multiAccountDNS(t, map[string]error{"hk": notOwned()})

	if err := app.runDNSList(context.Background(), "example.com"); err != nil {
		t.Fatalf("a foreign zone is an expected skip, not a failure: %v", err)
	}
	if out := app.testOut.String(); !strings.Contains(out, "203.0.113.10") {
		t.Fatalf("records from the owning account should be listed:\n%s", out)
	}
	errOut := app.testErr.String()
	if !strings.Contains(errOut, "hk") {
		t.Fatalf("the skipped account should be named:\n%s", errOut)
	}
	// The raw SDK struct must not be dumped once the code is known.
	if strings.Contains(errOut, "TencentCloudSDKError") {
		t.Fatalf("raw SDK error leaked into the output:\n%s", errOut)
	}
	if !strings.Contains(errOut, "DNSPod") {
		t.Fatalf("the hint should explain the ownership rule:\n%s", errOut)
	}
}

func TestDNSListFailsWhenNoAccountHostsTheZone(t *testing.T) {
	app := multiAccountDNS(t, map[string]error{
		"main": notOwned(),
		"hk":   notOwned(),
	})
	err := app.runDNSList(context.Background(), "example.com")
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Key != "dns.zone_not_hosted" {
		t.Fatalf("expected dns.zone_not_hosted, got %v", err)
	}
}

func TestDNSListStillFailsOnRealErrors(t *testing.T) {
	app := multiAccountDNS(t, map[string]error{
		"hk": tcerr.NewTencentCloudSDKError("UnauthorizedOperation.CamUnauthorized", "无权限", "req"),
	})
	err := app.runDNSList(context.Background(), "example.com")
	if err == nil {
		t.Fatal("a genuine permission failure must still be reported")
	}
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Partial {
		t.Fatalf("expected a partial failure, got %v", err)
	}
}
