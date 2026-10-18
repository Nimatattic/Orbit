package tencent

import (
	"encoding/json"
	"sort"
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

// Official CAM interface lists, per service. Every grantable action in the table
// must cite one of these so the generated policy can be traced back to the
// vendor's documentation.
const (
	SourceSSL = "https://cloud.tencent.com/document/product/598/98168"
	SourceCDN = "https://cloud.tencent.com/document/product/598/98110"
	SourceTEO = "https://cloud.tencent.com/document/product/598/99327"
)

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
	// NoGrant marks an API Orbit calls that needs no CAM authorization. Such an
	// action must never appear in the generated policy: the console rejects an
	// unknown action and refuses the whole document.
	NoGrant bool
	// Source is the official CAM interface list documenting the action.
	Source string
}

// permissions is the single source of truth for what Orbit asks for. The
// interface-driven test in permissions_test.go fails if the SDK interfaces and
// this table ever drift apart, and docs/cam/*.json is generated from it.
var permissions = []Permission{
	// --- always needed -------------------------------------------------------
	{
		// The identity probe needs no authorization: it answers "who am I" for
		// the calling credential. It is therefore informational here and is
		// excluded from the generated policy (an unknown action makes the CAM
		// console reject the whole document).
		Action: "sts:GetCallerIdentity", Tier: TierRead,
		PurposeKey: "perm.sts.identity", NoGrant: true,
	},
	{
		Action: "ssl:DescribeCertificates", Tier: TierRead, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.list", Source: SourceSSL,
	},
	{
		Action: "ssl:DescribeCertificateDetail", Tier: TierDelete, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.detail", Source: SourceSSL,
	},

	// --- CDN ----------------------------------------------------------------
	{
		Action: "cdn:DescribeDomainsConfig", Tier: TierRead, Scopes: []string{ScopeCDN},
		PurposeKey: "perm.cdn.read", Source: SourceCDN,
	},
	{
		Action: "cdn:ModifyDomainConfig", Tier: TierWrite, Scopes: []string{ScopeCDN},
		PurposeKey: "perm.cdn.write", Source: SourceCDN,
	},

	// --- EdgeOne ------------------------------------------------------------
	{
		Action: "teo:DescribeZones", Tier: TierRead, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.zones", Source: SourceTEO,
	},
	{
		Action: "teo:DescribeAccelerationDomains", Tier: TierRead, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.domains", Source: SourceTEO,
	},
	{
		Action: "teo:ModifyHostsCertificate", Tier: TierWrite, Scopes: []string{ScopeTEO},
		PurposeKey: "perm.teo.write", Source: SourceTEO,
	},

	// --- binding evidence (who uses which certificate) ----------------------
	{
		Action: "ssl:CreateCertificateBindResourceSyncTask", Tier: TierRead,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.binding_start", Source: SourceSSL,
	},
	{
		Action: "ssl:DescribeCertificateBindResourceTaskResult", Tier: TierRead,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.binding_result", Source: SourceSSL,
	},
	{
		Action: "ssl:DescribeHostCdnInstanceList", Tier: TierRead,
		Scopes: []string{ScopeCDN}, PurposeKey: "perm.ssl.host_cdn", Source: SourceSSL,
	},
	{
		Action: "ssl:DescribeHostTeoInstanceList", Tier: TierRead,
		Scopes: []string{ScopeTEO}, PurposeKey: "perm.ssl.host_teo", Source: SourceSSL,
	},

	// --- deletion -----------------------------------------------------------
	{
		Action: "ssl:DeleteCertificates", Tier: TierDelete, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.delete", Source: SourceSSL,
	},
	{
		Action: "ssl:DescribeDeleteCertificatesTaskResult", Tier: TierDelete,
		Scopes: []string{ScopeSSL}, PurposeKey: "perm.ssl.delete_task", Source: SourceSSL,
	},

	// --- optional: experimental unified deploy strategy ---------------------
	{
		Action: "ssl:DeployCertificateInstance", Tier: TierWrite, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.deploy", Source: SourceSSL, Experimental: true,
	},
	{
		Action: "ssl:DescribeHostDeployRecord", Tier: TierWrite, Scopes: []string{ScopeSSL},
		PurposeKey: "perm.ssl.deploy_record", Source: SourceSSL, Experimental: true,
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

// Policy is a Tencent Cloud CAM custom policy document.
//
// The shape follows the official grammar
// (https://cloud.tencent.com/document/product/598/10604):
//
//	policy    = { version, statement }
//	statement = { effect, action, resource, condition? }
//
// There is no "sid" in Tencent Cloud's grammar (unlike AWS), so it is
// deliberately omitted: the console validates the JSON and rejects unknown
// structure.
type Policy struct {
	Version   string            `json:"version"`
	Statement []PolicyStatement `json:"statement"`
}

// PolicyStatement is one statement of a CAM policy.
type PolicyStatement struct {
	Effect   string   `json:"effect"`
	Action   []string `json:"action"`
	Resource []string `json:"resource"`
}

// PolicyPermissions is the selection reduced to what a CAM policy may contain:
// actions that actually need a grant and are documented by the vendor. The
// console rejects an unknown action and refuses the whole document, so a
// no-grant API (sts:GetCallerIdentity) must be kept out.
func PolicyPermissions(f PermissionFilter) []Permission {
	out := make([]Permission, 0, len(permissions))
	for _, p := range SelectPermissions(f) {
		if p.NoGrant {
			continue
		}
		out = append(out, p)
	}
	return out
}

// PolicyDocument renders the selection as a ready-to-paste CAM policy: one
// statement per authority tier, so the operator can see the split and trim it.
func PolicyDocument(f PermissionFilter) Policy {
	grouped := map[PermissionTier][]string{}
	order := []PermissionTier{}
	for _, p := range PolicyPermissions(f) {
		if _, seen := grouped[p.Tier]; !seen {
			order = append(order, p.Tier)
		}
		grouped[p.Tier] = append(grouped[p.Tier], p.Action)
	}

	statements := make([]PolicyStatement, 0, len(order))
	for _, tier := range order {
		actions := grouped[tier]
		sort.Strings(actions)
		statements = append(statements, PolicyStatement{
			Effect:   "allow",
			Action:   actions,
			Resource: []string{"*"},
		})
	}
	return Policy{Version: "2.0", Statement: statements}
}

// PolicyJSON renders the policy as indented JSON, ready to paste into the
// console's "按策略语法创建" editor.
func PolicyJSON(f PermissionFilter) (string, error) {
	raw, err := json.MarshalIndent(PolicyDocument(f), "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw) + "\n", nil
}

// PolicyNameSuggestion is a starting name for the policy in the console.
func PolicyNameSuggestion(f PermissionFilter) string {
	switch f.MaxTier {
	case TierWrite:
		return "orbit-apply"
	case TierDelete:
		return "orbit-full"
	default:
		return "orbit-readonly"
	}
}
