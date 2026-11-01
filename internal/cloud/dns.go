package cloud

import (
	"context"

	"github.com/Nimatattic/Orbit/internal/core"
)

// DNSProvider is implemented by providers that can manage hosted DNS zones.
//
// It is a separate interface rather than part of Provider so that a provider (or a
// test double) that only deals with certificates stays unaffected: callers
// type-assert, exactly like MaterialFetcher for certificate export.
type DNSProvider interface {
	// ListZones returns the zones delegated to this provider.
	ListZones(ctx context.Context, account string) ([]core.DNSZone, error)
	// ListRecords returns every record of one zone.
	ListRecords(ctx context.Context, account, zone string) ([]core.DNSRecord, error)
	// CreateRecord adds a record and returns it with its assigned id.
	CreateRecord(ctx context.Context, account string, in core.DNSRecordInput) (core.DNSRecord, error)
	// UpdateRecord rewrites an existing record in place.
	UpdateRecord(ctx context.Context, account string, record core.DNSRecord) (core.DNSRecord, error)
	// DeleteRecord removes one record.
	DeleteRecord(ctx context.Context, account, zone string, recordID uint64) error
	// SetRecordEnabled enables or disables a record without deleting it.
	SetRecordEnabled(ctx context.Context, account, zone string, recordID uint64, enabled bool) error
}

// DNSResult is the outcome of one DNS write, shaped like core.Result so the same
// renderers and the same audit trail can be reused.
type DNSResult struct {
	Zone   string
	Record core.DNSRecord
	Op     string // create, update, delete, enable, disable
	OK     bool
	Err    error
}
