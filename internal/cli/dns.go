package cli

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/audit"
	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/render/tui"
	"github.com/Crequency/orbit/internal/version"
)

// dnsWriteFlags are the options shared by the mutating DNS commands.
type dnsWriteFlags struct {
	ttl      uint64
	mx       uint64
	line     string
	remark   string
	disabled bool
	recordID uint64
	server   string
}

// newDNSCmd builds the `orbit dns` command group.
func (a *App) newDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: a.Bundle.T("cmd.dns.short"),
		Long:  a.Bundle.T("cmd.dns.long"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSRoot(cmd.Context())
		},
	}
	cmd.PersistentFlags().StringVar(&a.Accounts, "accounts", "", "")
	// DNS writes follow the same safety rule as certificate writes.
	cmd.PersistentFlags().BoolVar(&a.Yes, "yes", false, "")
	cmd.PersistentFlags().BoolVar(&a.DryRun, "dry-run", false, "")

	cmd.AddCommand(
		a.newDNSZonesCmd(),
		a.newDNSListCmd(),
		a.newDNSSetCmd(),
		a.newDNSRemoveCmd(),
		a.newDNSToggleCmd(true),
		a.newDNSToggleCmd(false),
		a.newDNSCheckCmd(),
	)
	return cmd
}

// runDNSRoot opens the DNS console on a terminal, and lists zones otherwise.
func (a *App) runDNSRoot(ctx context.Context) error {
	if a.wantsTUI() {
		return a.tuiDNS(ctx)
	}
	return a.runDNSZones(ctx)
}

// --- shared plumbing -------------------------------------------------------

// dnsTargets resolves the accounts a command operates on.
func (a *App) dnsTargets() ([]string, error) {
	if v := strings.TrimSpace(a.Accounts); v != "" {
		return splitList(v), nil
	}
	aliases := a.knownAliases()
	if len(aliases) == 0 && a.Demo {
		return []string{DemoAccount}, nil
	}
	if len(aliases) == 0 {
		return nil, exitcode.New(exitcode.Fatal, "err.no_accounts")
	}
	return aliases, nil
}

// dnsProvider returns the DNS capability of one account's provider.
func (a *App) dnsProvider(account string) (cloud.DNSProvider, error) {
	provider, err := a.providerFor(account)
	if err != nil {
		return nil, err
	}
	dns, ok := provider.(cloud.DNSProvider)
	if !ok {
		return nil, exitcode.New(exitcode.Fatal, "err.dns_unavailable", "platform", provider.Name())
	}
	return dns, nil
}

// planWrite reports whether the command may write, or should only describe what
// it would do. DNS writes follow the same rule as certificate writes.
func (a *App) dnsWriteAllowed() bool {
	return a.Yes && !a.DryRun
}

// auditDNS records one DNS change in the append-only log.
func (a *App) auditDNS(account, op string, record core.DNSRecord, err error) {
	logger := a.auditLogger()
	if logger == nil {
		return
	}
	rec := audit.Record{
		RunID: a.runID, Version: version.Version, Kind: audit.KindChange,
		Command: "dns", Platform: DefaultPlatform, Account: account,
		Action: "dns." + op, Product: "dns", Domain: record.FQDN(),
		Result: "ok", Verified: false,
		Detail: fmt.Sprintf("%s %s id=%d value=%q ttl=%d", record.Type, record.Name, record.ID, record.Value, record.TTL),
	}
	if err != nil {
		rec.Result = "failed"
		rec.Error = err.Error()
	}
	_ = logger.Write(rec)
}

// verifyDNSRecord reads the zone back and confirms the record is visible. The
// provider's answer is not proof of propagation, but it does catch dropped writes.
func (a *App) verifyDNSRecord(ctx context.Context, dns cloud.DNSProvider, account string, want core.DNSRecord) bool {
	records, err := dns.ListRecords(ctx, account, want.Zone)
	if err != nil {
		return false
	}
	for _, got := range records {
		if got.ID != want.ID {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(got.Value), strings.TrimSpace(want.Value))
	}
	return false
}

// tuiDNS opens the interactive DNS console for one account.
func (a *App) tuiDNS(ctx context.Context) error {
	target, err := a.singleAccount()
	if err != nil {
		return err
	}
	dns, err := a.dnsProvider(target)
	if err != nil {
		return err
	}
	// The console owns the screen; keep provider progress off the terminal.
	a.progressFn = func(cloud.Progress) {}
	defer func() { a.progressFn = nil }()

	audit := func(op string, record core.DNSRecord, err error) { a.auditDNS(target, op, record, err) }

	job := tui.DNSJob{
		Account: target,
		Zones: func(c context.Context) ([]core.DNSZone, error) {
			return dns.ListZones(c, target)
		},
		Records: func(c context.Context, zone string) ([]core.DNSRecord, error) {
			return dns.ListRecords(c, target, zone)
		},
		Create: func(c context.Context, in core.DNSRecordInput) (core.DNSRecord, error) {
			record, err := dns.CreateRecord(c, target, in)
			audit("create", record, err)
			if err != nil {
				return core.DNSRecord{}, a.userError(a.mapProviderErr(err, "tencent"))
			}
			return record, nil
		},
		Update: func(c context.Context, record core.DNSRecord) (core.DNSRecord, error) {
			updated, err := dns.UpdateRecord(c, target, record)
			audit("update", updated, err)
			if err != nil {
				return core.DNSRecord{}, a.userError(a.mapProviderErr(err, "tencent"))
			}
			return updated, nil
		},
		Delete: func(c context.Context, zone string, id uint64) error {
			err := dns.DeleteRecord(c, target, zone, id)
			audit("delete", core.DNSRecord{ID: id, Zone: zone}, err)
			if err != nil {
				return a.userError(a.mapProviderErr(err, "tencent"))
			}
			return nil
		},
		Toggle: func(c context.Context, zone string, id uint64, enabled bool) error {
			err := dns.SetRecordEnabled(c, target, zone, id, enabled)
			op := "disable"
			if enabled {
				op = "enable"
			}
			audit(op, core.DNSRecord{ID: id, Zone: zone}, err)
			if err != nil {
				return a.userError(a.mapProviderErr(err, "tencent"))
			}
			return nil
		},
	}
	return tui.RunDNS(ctx, a.Bundle, job)
}

// --- zones -----------------------------------------------------------------

func (a *App) newDNSZonesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zones",
		Short: a.Bundle.T("cmd.dns.zones.short"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSZones(cmd.Context())
		},
	}
}

// dnsZoneRow is one line of the zone listing.
type dnsZoneRow struct {
	Account string       `json:"account"`
	Zone    core.DNSZone `json:"zone"`
}

func (a *App) runDNSZones(ctx context.Context) error {
	targets, err := a.dnsTargets()
	if err != nil {
		return err
	}
	var rows []dnsZoneRow
	failures := 0
	for _, account := range targets {
		dns, err := a.dnsProvider(account)
		if err != nil {
			fmt.Fprintln(a.Err, a.Bundle.T("dns.account_failed", "account", account, "error", a.userError(err)))
			failures++
			continue
		}
		zones, err := dns.ListZones(ctx, account)
		if err != nil {
			fmt.Fprintln(a.Err, a.Bundle.T("dns.account_failed", "account", account, "error", a.userError(err)))
			failures++
			continue
		}
		for _, z := range zones {
			rows = append(rows, dnsZoneRow{Account: account, Zone: z})
		}
	}

	if a.JSON {
		if err := a.jsonOut(rows); err != nil {
			return err
		}
	} else if len(rows) == 0 {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.no_zones"))
	} else {
		fmt.Fprintf(a.Out, "%s %s %s %s %s\n",
			padText(a.Bundle.T("dns.col_zone"), 28), padText(a.Bundle.T("dns.col_status"), 10),
			padText(a.Bundle.T("dns.col_records"), 8), padText(a.Bundle.T("dns.col_grade"), 10),
			a.Bundle.T("dns.col_account"))
		for _, r := range rows {
			status := r.Zone.DNSStatus
			if status == "" {
				status = r.Zone.Status
			}
			fmt.Fprintf(a.Out, "%s %s %s %s %s\n",
				padText(r.Zone.Name, 28), padText(strings.ToLower(status), 10),
				padText(fmt.Sprintf("%d", r.Zone.RecordCount), 8), padText(r.Zone.Grade, 10),
				r.Account)
		}
	}
	return a.partialFailure(failures)
}

// --- records ---------------------------------------------------------------

func (a *App) newDNSListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls <zone>",
		Short: a.Bundle.T("cmd.dns.ls.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSList(cmd.Context(), args[0])
		},
	}
	cmd.Flags().StringVar(&a.DNSFilterType, "type", "", "")
	cmd.Flags().StringVar(&a.DNSFilterName, "name", "", "")
	cmd.Flags().StringVar(&a.DNSFilterKeyword, "keyword", "", "")
	return cmd
}

func (a *App) runDNSList(ctx context.Context, zone string) error {
	targets, err := a.dnsTargets()
	if err != nil {
		return err
	}
	zone = core.NormaliseZone(zone)
	filter := core.DNSRecordFilter{Type: a.DNSFilterType, Name: a.DNSFilterName, Keyword: a.DNSFilterKeyword}

	var all []core.DNSRecord
	failures := 0
	for _, account := range targets {
		dns, err := a.dnsProvider(account)
		if err != nil {
			fmt.Fprintln(a.Err, a.Bundle.T("dns.account_failed", "account", account, "error", a.userError(err)))
			failures++
			continue
		}
		records, err := dns.ListRecords(ctx, account, zone)
		if err != nil {
			fmt.Fprintln(a.Err, a.Bundle.T("dns.account_failed", "account", account, "error", a.userError(err)))
			failures++
			continue
		}
		all = append(all, core.FilterDNSRecords(records, filter)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].Type < all[j].Type
	})

	if a.JSON {
		if err := a.jsonOut(all); err != nil {
			return err
		}
		return a.partialFailure(failures)
	}
	if len(all) == 0 {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.no_records", "zone", zone))
		return a.partialFailure(failures)
	}
	fmt.Fprintf(a.Out, "%-8s %-22s %-6s %-38s %-7s %-6s %-8s %s\n",
		a.Bundle.T("dns.col_id"), a.Bundle.T("dns.col_name"), a.Bundle.T("dns.col_type"),
		a.Bundle.T("dns.col_value"), a.Bundle.T("dns.col_ttl"), "MX",
		a.Bundle.T("dns.col_state"), a.Bundle.T("dns.col_remark"))
	for _, r := range all {
		fmt.Fprintf(a.Out, "%-8d %-22s %-6s %-38s %-7d %-6d %-8s %s\n",
			r.ID, r.Name, r.Type, truncateText(r.Value, 38), r.TTL, r.MX,
			dnsStateLabel(r.Enabled), r.Remark)
	}
	return a.partialFailure(failures)
}

func dnsStateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// --- set (upsert) ----------------------------------------------------------

func (a *App) newDNSSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <zone> <name> <type> <value>",
		Short: a.Bundle.T("cmd.dns.set.short"),
		Long:  a.Bundle.T("cmd.dns.set.long"),
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSSet(cmd.Context(), args[0], args[1], args[2], args[3])
		},
	}
	f := &a.DNSFlags
	cmd.Flags().Uint64Var(&f.ttl, "ttl", core.DefaultDNSTTL, "")
	cmd.Flags().Uint64Var(&f.mx, "mx", 10, "")
	cmd.Flags().StringVar(&f.line, "line", "", "")
	cmd.Flags().StringVar(&f.remark, "remark", "", "")
	cmd.Flags().BoolVar(&f.disabled, "disabled", false, "")
	cmd.Flags().Uint64Var(&f.recordID, "record-id", 0, "")
	return cmd
}

func (a *App) runDNSSet(ctx context.Context, zone, name, typ, value string) error {
	zone = core.NormaliseZone(zone)
	in := core.DNSRecordInput{
		Zone: zone, Name: core.NormaliseDNSName(name), Type: strings.ToUpper(typ),
		Value: strings.TrimSpace(value), TTL: a.DNSFlags.ttl, MX: a.DNSFlags.mx,
		Line: a.DNSFlags.line, Remark: a.DNSFlags.remark, Enabled: !a.DNSFlags.disabled,
	}
	in = in.WithDefaults()
	if err := core.ValidateRecordInput(in); err != nil {
		return exitcode.New(exitcode.Fatal, "dns.invalid_record", "error", err.Error())
	}
	target, err := a.singleAccount()
	if err != nil {
		return err
	}
	dns, err := a.dnsProvider(target)
	if err != nil {
		return err
	}

	records, err := dns.ListRecords(ctx, target, zone)
	if err != nil {
		return a.mapProviderErr(err, "tencent")
	}

	// An explicit record id turns `set` into "rewrite this record".
	if a.DNSFlags.recordID != 0 {
		existing, ok := core.FindDNSRecord(records, a.DNSFlags.recordID)
		if !ok {
			return exitcode.New(exitcode.Fatal, "dns.record_missing", "id", a.DNSFlags.recordID, "zone", zone)
		}
		return a.applyDNSUpdate(ctx, dns, target, existing, in, true)
	}

	change, err := core.PlanUpsert(records, in)
	if err != nil {
		return exitcode.New(exitcode.Fatal, "dns.ambiguous", "error", err.Error())
	}
	switch change.Kind {
	case core.DNSKeep:
		fmt.Fprintln(a.Out, a.Bundle.T("dns.keep", "fqdn", core.RecordFQDN(zone, in.Name), "type", in.Type))
		return nil
	case core.DNSCreate:
		return a.applyDNSCreate(ctx, dns, target, in)
	default:
		return a.applyDNSUpdate(ctx, dns, target, *change.Existing, in, false)
	}
}

func (a *App) applyDNSCreate(ctx context.Context, dns cloud.DNSProvider, account string, in core.DNSRecordInput) error {
	fqdn := core.RecordFQDN(in.Zone, in.Name)
	if !a.dnsWriteAllowed() {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.plan_create", "fqdn", fqdn, "type", in.Type, "value", in.Value, "ttl", in.TTL))
		return a.dnsDryRunHint()
	}
	record, err := dns.CreateRecord(ctx, account, in)
	if err != nil {
		a.auditDNS(account, "create", core.DNSRecord{Zone: in.Zone, Name: in.Name, Type: in.Type, Value: in.Value}, err)
		return a.mapProviderErr(err, "tencent")
	}
	verified := a.verifyDNSRecord(ctx, dns, account, record)
	a.auditDNS(account, "create", record, nil)
	fmt.Fprintln(a.Out, a.Bundle.T("dns.created", "fqdn", fqdn, "type", record.Type, "id", record.ID,
		"state", dnsVerifiedLabel(verified)))
	return nil
}

func (a *App) applyDNSUpdate(ctx context.Context, dns cloud.DNSProvider, account string, existing core.DNSRecord, in core.DNSRecordInput, explicit bool) error {
	fqdn := core.RecordFQDN(in.Zone, in.Name)
	if !a.dnsWriteAllowed() {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.plan_update", "fqdn", fqdn, "type", in.Type,
			"from", existing.Value, "to", in.Value))
		return a.dnsDryRunHint()
	}
	desired := existing
	desired.Name, desired.Type, desired.Value = in.Name, in.Type, in.Value
	desired.TTL, desired.MX, desired.Line = in.TTL, in.MX, in.Line
	desired.Remark, desired.Enabled = in.Remark, in.Enabled

	updated, err := dns.UpdateRecord(ctx, account, desired)
	if err != nil {
		a.auditDNS(account, "update", desired, err)
		return a.mapProviderErr(err, "tencent")
	}
	verified := a.verifyDNSRecord(ctx, dns, account, updated)
	a.auditDNS(account, "update", updated, nil)
	fmt.Fprintln(a.Out, a.Bundle.T("dns.updated", "fqdn", fqdn, "type", updated.Type, "id", updated.ID,
		"state", dnsVerifiedLabel(verified)))
	return nil
}

func dnsVerifiedLabel(verified bool) string {
	if verified {
		return "verified"
	}
	return "unverified"
}

// dnsDryRunHint prints how to actually perform the change.
func (a *App) dnsDryRunHint() error {
	fmt.Fprintln(a.Out, a.Bundle.T("dns.dry_run_hint"))
	return nil
}

// --- remove / toggle -------------------------------------------------------

func (a *App) newDNSRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <zone> <record-id...>",
		Short: a.Bundle.T("cmd.dns.rm.short"),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSRemove(cmd.Context(), args[0], args[1:])
		},
	}
	return cmd
}

func (a *App) runDNSRemove(ctx context.Context, zone string, ids []string) error {
	zone = core.NormaliseZone(zone)
	target, err := a.singleAccount()
	if err != nil {
		return err
	}
	dns, err := a.dnsProvider(target)
	if err != nil {
		return err
	}
	records, err := dns.ListRecords(ctx, target, zone)
	if err != nil {
		return a.mapProviderErr(err, "tencent")
	}
	var victims []core.DNSRecord
	for _, raw := range ids {
		id, parseErr := parseUint(raw)
		if parseErr != nil {
			return exitcode.New(exitcode.Fatal, "dns.bad_id", "value", raw)
		}
		record, ok := core.FindDNSRecord(records, id)
		if !ok {
			return exitcode.New(exitcode.Fatal, "dns.record_missing", "id", id, "zone", zone)
		}
		victims = append(victims, record)
	}
	for _, record := range victims {
		if !a.dnsWriteAllowed() {
			fmt.Fprintln(a.Out, a.Bundle.T("dns.plan_delete", "fqdn", record.FQDN(), "type", record.Type, "value", record.Value))
			continue
		}
		if err := dns.DeleteRecord(ctx, target, zone, record.ID); err != nil {
			a.auditDNS(target, "delete", record, err)
			return a.mapProviderErr(err, "tencent")
		}
		a.auditDNS(target, "delete", record, nil)
		fmt.Fprintln(a.Out, a.Bundle.T("dns.deleted", "fqdn", record.FQDN(), "type", record.Type, "id", record.ID))
	}
	if !a.dnsWriteAllowed() {
		return a.dnsDryRunHint()
	}
	return nil
}

func (a *App) newDNSToggleCmd(enable bool) *cobra.Command {
	use, key := "disable <zone> <record-id...>", "cmd.dns.disable.short"
	if enable {
		use, key = "enable <zone> <record-id...>", "cmd.dns.enable.short"
	}
	return &cobra.Command{
		Use:   use,
		Short: a.Bundle.T(key),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSToggle(cmd.Context(), args[0], args[1:], enable)
		},
	}
}

func (a *App) runDNSToggle(ctx context.Context, zone string, ids []string, enable bool) error {
	zone = core.NormaliseZone(zone)
	target, err := a.singleAccount()
	if err != nil {
		return err
	}
	dns, err := a.dnsProvider(target)
	if err != nil {
		return err
	}
	records, err := dns.ListRecords(ctx, target, zone)
	if err != nil {
		return a.mapProviderErr(err, "tencent")
	}
	op := "disable"
	if enable {
		op = "enable"
	}
	for _, raw := range ids {
		id, parseErr := parseUint(raw)
		if parseErr != nil {
			return exitcode.New(exitcode.Fatal, "dns.bad_id", "value", raw)
		}
		record, ok := core.FindDNSRecord(records, id)
		if !ok {
			return exitcode.New(exitcode.Fatal, "dns.record_missing", "id", id, "zone", zone)
		}
		if !a.dnsWriteAllowed() {
			fmt.Fprintln(a.Out, a.Bundle.T("dns.plan_toggle", "op", op, "fqdn", record.FQDN(), "type", record.Type))
			continue
		}
		if err := dns.SetRecordEnabled(ctx, target, zone, id, enable); err != nil {
			a.auditDNS(target, op, record, err)
			return a.mapProviderErr(err, "tencent")
		}
		record.Enabled = enable
		a.auditDNS(target, op, record, nil)
		fmt.Fprintln(a.Out, a.Bundle.T("dns.toggled", "op", op, "fqdn", record.FQDN(), "type", record.Type, "id", id))
	}
	if !a.dnsWriteAllowed() {
		return a.dnsDryRunHint()
	}
	return nil
}

// --- check (propagation) ---------------------------------------------------

func (a *App) newDNSCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <zone> <name> <type>",
		Short: a.Bundle.T("cmd.dns.check.short"),
		Long:  a.Bundle.T("cmd.dns.check.long"),
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSCheck(cmd.Context(), args[0], args[1], args[2])
		},
	}
	cmd.Flags().StringVar(&a.DNSFlags.server, "server", "", "")
	return cmd
}

// runDNSCheck compares what the provider holds with what public DNS answers.
func (a *App) runDNSCheck(ctx context.Context, zone, name, typ string) error {
	zone = core.NormaliseZone(zone)
	fqdn := core.RecordFQDN(zone, name)
	typ = strings.ToUpper(typ)
	target, err := a.singleAccount()
	if err != nil {
		return err
	}
	dns, err := a.dnsProvider(target)
	if err != nil {
		return err
	}
	records, err := dns.ListRecords(ctx, target, zone)
	if err != nil {
		return a.mapProviderErr(err, "tencent")
	}
	wanted := core.FilterDNSRecords(records, core.DNSRecordFilter{Name: name, Type: typ})
	if len(wanted) == 0 {
		return exitcode.New(exitcode.Fatal, "dns.record_missing_name", "fqdn", fqdn, "type", typ)
	}

	resolver := a.dnsResolver()
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	answers, err := resolveValues(lookupCtx, resolver, fqdn, typ)
	if err != nil {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.check_lookup_failed", "fqdn", fqdn, "error", err.Error()))
	}

	var missing []string
	for _, rec := range wanted {
		if !containsAnswer(answers, rec.Value) {
			missing = append(missing, rec.Value)
		}
	}
	if a.JSON {
		return a.jsonOut(map[string]any{
			"zone": zone, "fqdn": fqdn, "type": typ,
			"configured": valuesOf(wanted), "answers": answers, "missing": missing,
		})
	}
	fmt.Fprintf(a.Out, "%s %s\n", a.Bundle.T("dns.check_configured"), strings.Join(valuesOf(wanted), ", "))
	if len(answers) > 0 {
		fmt.Fprintf(a.Out, "%s %s\n", a.Bundle.T("dns.check_answers"), strings.Join(answers, ", "))
	}
	if len(missing) == 0 {
		fmt.Fprintln(a.Out, a.Bundle.T("dns.check_ok", "fqdn", fqdn))
		return nil
	}
	fmt.Fprintln(a.Out, a.Bundle.T("dns.check_missing", "fqdn", fqdn, "values", strings.Join(missing, ", ")))
	return exitcode.New(exitcode.PendingChanges, "dns.check_pending", "fqdn", fqdn)
}

// dnsResolver returns the resolver used by `dns check`; --server overrides it.
func (a *App) dnsResolver() *net.Resolver {
	if server := strings.TrimSpace(a.DNSFlags.server); server != "" {
		addr := server
		if !strings.Contains(addr, ":") {
			addr += ":53"
		}
		return &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", addr)
			},
		}
	}
	return net.DefaultResolver
}

// resolveValues queries the requested type and returns the answers as strings.
func resolveValues(ctx context.Context, r *net.Resolver, fqdn, typ string) ([]string, error) {
	switch typ {
	case core.DNSTypeA, core.DNSTypeAAAA:
		ips, err := r.LookupIP(ctx, "ip", fqdn)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			if typ == core.DNSTypeA && ip.To4() == nil {
				continue
			}
			if typ == core.DNSTypeAAAA && ip.To4() != nil {
				continue
			}
			out = append(out, ip.String())
		}
		return out, nil
	case core.DNSTypeCNAME:
		cname, err := r.LookupCNAME(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		cname = strings.TrimSuffix(cname, ".")
		// The resolver echoes the queried name when no CNAME exists; that is not
		// an answer, it is the absence of one.
		if strings.EqualFold(cname, fqdn) {
			return nil, nil
		}
		return []string{cname}, nil
	case core.DNSTypeTXT:
		records, err := r.LookupTXT(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		return records, nil
	case core.DNSTypeMX:
		records, err := r.LookupMX(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(records))
		for _, mx := range records {
			out = append(out, fmt.Sprintf("%d %s", mx.Pref, strings.TrimSuffix(mx.Host, ".")))
		}
		return out, nil
	case core.DNSTypeNS:
		records, err := r.LookupNS(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(records))
		for _, ns := range records {
			out = append(out, strings.TrimSuffix(ns.Host, "."))
		}
		return out, nil
	}
	return nil, fmt.Errorf("propagation checks do not support %s", typ)
}

// containsAnswer reports whether a value appears among the resolver answers.
func containsAnswer(answers []string, want string) bool {
	want = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(want), "."))
	for _, got := range answers {
		if strings.EqualFold(strings.TrimSuffix(got, "."), want) {
			return true
		}
	}
	return false
}

func valuesOf(records []core.DNSRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.Value)
	}
	return out
}

// --- helpers ---------------------------------------------------------------

// singleAccount resolves exactly one account for a write, refusing ambiguity.
func (a *App) singleAccount() (string, error) {
	targets, err := a.dnsTargets()
	if err != nil {
		return "", err
	}
	if len(targets) != 1 {
		return "", exitcode.New(exitcode.GateRefused, "dns.one_account",
			"accounts", strings.Join(targets, ", "))
	}
	return targets[0], nil
}

// padText pads s to width display columns, so CJK values stay aligned.
func padText(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// parseUint parses a command-line record id.
func parseUint(raw string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
}

// partialFailure maps a failure count to the exit code contract.
func (a *App) partialFailure(failures int) error {
	if failures > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	return nil
}
