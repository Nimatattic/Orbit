package tencent

import (
	"sort"
	"strings"
)

// PermissionTier groups actions by how much authority they carry, so an operator
// can grant exactly the level they intend to use.
type PermissionTier string

// Tiers, ordered from least to most authority.
const (
	// TierRead powers status, the plan and the self-test.
	TierRead PermissionTier = "read"
	// TierWrite applies a certificate to a domain.
	TierWrite PermissionTier = "write"
	// TierDelete removes a certificate from the account.
	TierDelete PermissionTier = "delete"
)

// ServiceRole is the linked role Tencent Cloud needs for certificate deployment
// and for the binding checks that gate deletion. It is granted once per account
// in the console, not through a CAM policy statement.
const ServiceRole = "SSL_QCSLinkedRoleInReplaceLoadCertificate"

// ProductScope identifies a scope the operator can opt into. The certificate
// inventory is always needed; the domain products are optional.
const (
	// ScopeSSL is the certificate pool itself: always required.
	ScopeSSL = "ssl"
	// ScopeCDN covers cdn:DescribeDomainsConfig / cdn:ModifyDomainConfig.
	ScopeCDN = "cdn"
	// ScopeTEO covers EdgeOne (teo:*).
	ScopeTEO = "teo"
)

// Permission is one CAM action Orbit uses, with the reason it needs it.
type Permission struct {
	// Action is the CAM action name, "<service>:<Action>".
	Action string
	// Tier is the least authority level that requires this action.
	Tier PermissionTier
	// Scopes lists the product scopes that require the action. Empty means it is
	// always required.
	Scopes []string
	// PurposeKey is the i18n key explaining what Orbit does with it.
	PurposeKey string
	// Experimental marks actions only used by the experimental unified deploy
	// strategy (--strategy=deploy); they are listed separately.
	Experimental bool
}

// permissions is the single source of truth for what Orbit asks for. The
// interface-driven test in permissions_test.go fails if the SDK interfaces and
// this table ever drift apart, and docs/cam/*.json is generated from it.
var permissions = []Permission{
	// --- always needed -------------------------------------------------------
	{
		Action: "sts:GetCallerIdentity", Tier: TierRead,
		PurposeKey: "perm.sts.identity",
	},
	{
		Action: "ssl:DescribeCertificates", Tier: TierRead, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.list",
	},
	{
		Action: "ssl:DescribeCertificateDetail", Tier: TierDelete, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.detail",
	},

	// --- CDN ----------------------------------------------------------------
	{
		Action: "cdn:DescribeDomainsConfig", Tier: TierRead, Scopes: []string{ScopeCDN},
		PurposeKey: "perm.cdn.read",
	},
	{
		Action: "cdn:ModifyDomainConfig", Tier: TierWrite, Scopes: []string{ScopeCDN},
		PurposeKey: "perm.cdn.write",
	},

	// --- EdgeOne ------------------------------------------------------------
	{
		Action: "teo:DescribeZones", Tier: TierRead, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.zones",
	},
	{
		Action: "teo:DescribeAccelerationDomains", Tier: TierRead, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.domains",
	},
	{
		Action: "teo:ModifyHostsCertificate", Tier: TierWrite, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.write",
	},

	// --- binding evidence (who uses which certificate) ----------------------
	{
		Action: "ssl:CreateCertificateBindResourceSyncTask", Tier: TierRead,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.binding_start",
	},
	{
		Action: "ssl:DescribeCertificateBindResourceTaskResult", Tier: TierRead,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.binding_result",
	},
	{
		Action: "ssl:DescribeHostCdnInstanceList", Tier: TierRead,
		Scopes: []string{ScopeCDN}, PurposeKey: "perm.ssl.host_cdn",
	},
	{
		Action: "ssl:DescribeHostTeoInstanceList", Tier: TierRead,
		Scopes: []string{ScopeTEO}, PurposeKey: "perm.ssl.host_teo",
	},

	// --- deletion -----------------------------------------------------------
	{
		Action: "ssl:DeleteCertificates", Tier: TierDelete, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.delete",
	},
	{
		Action: "ssl:DescribeDeleteCertificatesTaskResult", Tier: TierDelete,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.delete_task",
	},

	// --- optional: experimental unified deploy strategy ---------------------
	{
		Action: "ssl:DeployCertificateInstance", Tier: TierWrite, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.deploy", Experimental: true,
	},
	{
		Action: "ssl:DescribeHostDeployRecord", Tier: TierWrite, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.deploy_record", Experimental: true,
	},
}

// Permissions exposes the table (a copy, so callers cannot mutate it).
func Permissions() []Permission {
	out := make([]Permission, len(permissions))
	copy(out, permissions)
	return out
}

// PermissionFilter narrows the table to one operator's choices.
type PermissionFilter struct {
	// Scopes selected by the operator. Empty means "all scopes".
	Scopes []string
	// MaxTier is the highest authority the operator wants.
	MaxTier PermissionTier
	// IncludeExperimental adds the experimental deploy actions.
	IncludeExperimental bool
}

// TierLevel orders tiers so callers can compare them.
func TierLevel(t PermissionTier) int {
	switch t {
	case TierRead:
		return 1
	case TierWrite:
		return 2
	case TierDelete:
		return 3
	}
	return 0
}

// SelectPermissions returns the actions an operator with this scope needs,
// sorted by tier then action name.
func SelectPermissions(f PermissionFilter) []Permission {
	scopes := map[string]bool{}
	for _, s := range f.Scopes {
		scopes[s] = true
	}
	maxTier := f.MaxTier
	if maxTier == "" {
		maxTier = TierDelete
	}

	var out []Permission
	for _, p := range permissions {
		if p.Experimental && !f.IncludeExperimental {
			continue
		}
		if TierLevel(p.Tier) > TierLevel(maxTier) {
			continue
		}
		if len(scopes) > 0 && len(p.Scopes) > 0 {
			matched := false
			for _, s := range p.Scopes {
				if scopes[s] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return TierLevel(out[i].Tier) < TierLevel(out[j].Tier)
		}
		return out[i].Action < out[j].Action
	})
	return out
}

// PolicyDocument renders the selection as a ready-to-paste CAM policy.
func PolicyDocument(f PermissionFilter) map[string]any {
	grouped := map[PermissionTier][]string{}
	order := []PermissionTier{}
	for _, p := range SelectPermissions(f) {
		if _, seen := grouped[p.Tier]; !seen {
			order = append(order, p.Tier)
		}
		grouped[p.Tier] = append(grouped[p.Tier], p.Action)
	}

	statements := make([]any, 0, len(order))
	for _, tier := range order {
		statements = append(statements, map[string]any{
			"sid":      "orbit-" + strings.ToLower(string(tier)),
			"effect":   "allow",
			"action":   grouped[tier],
			"resource": []string{"*"},
		})
	}
	return map[string]any{
		"version":   "2.0",
		"statement": statements,
	}
}
