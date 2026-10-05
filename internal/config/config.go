// Package config loads Orbit's layered configuration.
//
// Precedence (plan §6.2): command-line flag > ORBIT_* environment >
// --config file > ./.orbit.yaml (project) > ~/.orbit/config.yaml (global) >
// built-in defaults. Merging happens on raw YAML maps, so a project file only
// needs to state what it overrides (decision B4).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentVersion is the schema version Orbit writes.
const CurrentVersion = 1

// Products supported by this batch (decision A3).
var SupportedProducts = []string{"cdn", "teo"}

// Strategies accepted by --strategy (plan §2.2).
var Strategies = []string{"auto", "deploy", "product"}

// Config is the fully merged configuration.
type Config struct {
	Version   int                 `yaml:"version"`
	Language  string              `yaml:"language"`
	Defaults  Defaults            `yaml:"defaults"`
	Platforms map[string]Platform `yaml:"platforms"`

	path string // resolved baseline path, for messages
}

// Defaults holds cross-account settings.
type Defaults struct {
	Platforms         []string    `yaml:"platforms"`
	Products          []string    `yaml:"products"`
	Strategy          string      `yaml:"strategy"`
	Concurrency       Concurrency `yaml:"concurrency"`
	Timeouts          Timeouts    `yaml:"timeouts"`
	CertPreference    []string    `yaml:"cert_preference"`
	DomainMatch       DomainMatch `yaml:"domain_match"`
	ExperimentalWrite bool        `yaml:"experimental_write"`
	EdgeOne           EdgeOne     `yaml:"edgeone"`
	Clean             Clean       `yaml:"clean"`
}

// Concurrency bounds parallel work.
type Concurrency struct {
	Products int `yaml:"products"`
	Changes  int `yaml:"changes"`
}

// Timeouts keeps durations as strings so YAML stays readable ("15s", "5m").
type Timeouts struct {
	API        string `yaml:"api"`
	DeployPoll string `yaml:"deploy_poll"`
}

// DomainMatch controls matching behaviour.
type DomainMatch struct {
	Wildcard     bool `yaml:"wildcard"`
	AllowExpired bool `yaml:"allow_expired"`
}

// EdgeOne holds product specific switches.
type EdgeOne struct {
	ReplaceFreeCert bool `yaml:"replace_free_cert"`
}

// Clean configures the cleanup gates (G1–G4).
type Clean struct {
	OlderThan           string   `yaml:"older_than"`
	RequireFullEvidence bool     `yaml:"require_full_evidence"`
	IncludeFree         bool     `yaml:"include_free"`
	ProtectPatterns     []string `yaml:"protect_patterns"`
	// Scope is reserved for the next batch (decision B3): today every
	// FilterSource=upload certificate is a deletion candidate.
	Scope Scope `yaml:"scope"`
}

// Scope narrows which certificates Orbit may touch (next batch).
type Scope struct {
	CertAliasPrefixes []string          `yaml:"cert_alias_prefixes"`
	CertTags          map[string]string `yaml:"cert_tags"`
	Domains           []string          `yaml:"domains"`
}

// Platform groups accounts of one cloud vendor.
type Platform struct {
	Accounts []Account `yaml:"accounts"`
}

// Account is one set of credentials plus its scope.
type Account struct {
	Alias      string        `yaml:"alias"`
	Credential CredentialRef `yaml:"credential"`
	Products   []string      `yaml:"products"`
	Regions    []string      `yaml:"regions"` // reserved: v1 needs no Region
	Scope      Scope         `yaml:"scope"`
}

// CredentialRef says where an account's secrets come from. Orbit never reads
// ~/.tccli (decision A1).
type CredentialRef struct {
	Source       string `yaml:"source"` // store | env
	SecretIDEnv  string `yaml:"secret_id_env"`
	SecretKeyEnv string `yaml:"secret_key_env"`
}

// Options controls Load.
type Options struct {
	// Explicit is the --config path; when set it replaces the global baseline.
	Explicit string
	// UseLocal forces the project file to exist (--local).
	UseLocal bool
	// WorkDir defaults to the process working directory.
	WorkDir string
	// Home defaults to config.Home().
	Home string
}

// Loaded is the result of Load, including which files participated.
type Loaded struct {
	Config     *Config
	GlobalPath string
	LocalPath  string
	Used       []string
}

// Load reads, merges and validates configuration.
func Load(opts Options) (*Loaded, error) {
	home := opts.Home
	if home == "" {
		home = Home()
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "."
	}
	globalPath := filepath.Join(home, "config.yaml")
	localPath := filepath.Join(workDir, ".orbit.yaml")

	// Composition order implements the documented precedence:
	//   --config > ./.orbit.yaml > ~/.orbit/config.yaml > built-in defaults
	// so the baseline is merged first and every higher-precedence layer is
	// merged over it.
	merged := map[string]any{}
	used := []string{}

	global, globalExists, err := readMap(globalPath)
	if err != nil {
		return nil, err
	}
	if globalExists && opts.Explicit == "" {
		merged = merge(merged, global)
		used = append(used, globalPath)
	}

	local, localExists, err := readMap(localPath)
	if err != nil {
		return nil, err
	}
	if opts.UseLocal && !localExists {
		return nil, fmt.Errorf("project configuration %s not found", localPath)
	}
	if localExists {
		merged = merge(merged, local)
		used = append(used, localPath)
	}

	baselinePath := globalPath
	if opts.Explicit != "" {
		explicit, exists, err := readMap(opts.Explicit)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("configuration file %s not found", opts.Explicit)
		}
		merged = merge(merged, explicit)
		used = append(used, opts.Explicit)
		baselinePath = opts.Explicit
	}

	applyDefaults(merged)

	raw, err := yaml.Marshal(merged)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	cfg.path = baselinePath
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Loaded{Config: cfg, GlobalPath: globalPath, LocalPath: localPath, Used: used}, nil
}

// Validate checks invariants that would otherwise fail deep inside a run.
func (c *Config) Validate() error {
	if c.Version != 0 && c.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d", c.Version)
	}
	if err := ValidateStrategy(c.Defaults.Strategy); err != nil {
		return err
	}
	if err := ValidateProducts(c.Defaults.Products); err != nil {
		return err
	}
	if _, err := ParseDuration(c.Defaults.Clean.OlderThan); err != nil {
		return err
	}
	if _, err := ParseDuration(c.Defaults.Timeouts.API); err != nil {
		return err
	}
	if _, err := ParseDuration(c.Defaults.Timeouts.DeployPoll); err != nil {
		return err
	}
	seen := map[string]bool{}
	for platform, p := range c.Platforms {
		for i, acc := range p.Accounts {
			if strings.TrimSpace(acc.Alias) == "" {
				return fmt.Errorf("platforms.%s.accounts[%d]: alias is required", platform, i)
			}
			key := platform + "/" + acc.Alias
			if seen[key] {
				return fmt.Errorf("duplicate account %s", key)
			}
			seen[key] = true
			if len(acc.Products) > 0 {
				if err := ValidateProducts(acc.Products); err != nil {
					return fmt.Errorf("account %s: %w", key, err)
				}
			}
			switch acc.Credential.Source {
			case "", "store", "env":
			default:
				return fmt.Errorf("account %s: unknown credential source %q", key, acc.Credential.Source)
			}
			if acc.Credential.Source == "env" && (acc.Credential.SecretIDEnv == "" || acc.Credential.SecretKeyEnv == "") {
				return fmt.Errorf("account %s: credential source env requires secret_id_env and secret_key_env", key)
			}
		}
	}
	return nil
}

// Path returns the baseline configuration path that was loaded.
func (c *Config) Path() string { return c.path }

// Accounts returns the accounts of one platform, in declaration order.
func (c *Config) Accounts(platform string) []Account {
	if c.Platforms == nil {
		return nil
	}
	return c.Platforms[platform].Accounts
}

// Account looks up one account by alias.
func (c *Config) Account(platform, alias string) (Account, bool) {
	for _, a := range c.Accounts(platform) {
		if a.Alias == alias {
			return a, true
		}
	}
	return Account{}, false
}

// ProductsFor resolves the effective product list for an account.
func (c *Config) ProductsFor(acc Account) []string {
	if len(acc.Products) > 0 {
		return acc.Products
	}
	if len(c.Defaults.Products) > 0 {
		return c.Defaults.Products
	}
	return SupportedProducts
}

// ValidateProducts rejects products outside this batch.
func ValidateProducts(products []string) error {
	for _, p := range products {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		ok := false
		for _, s := range SupportedProducts {
			if p == s {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("unsupported product %q (supported: %s)", p, strings.Join(SupportedProducts, ", "))
		}
	}
	return nil
}

// ValidateStrategy rejects unknown strategies.
func ValidateStrategy(s string) error {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return nil
	}
	for _, want := range Strategies {
		if s == want {
			return nil
		}
	}
	return fmt.Errorf("unknown strategy %q (supported: %s)", s, strings.Join(Strategies, ", "))
}

// ParseDuration accepts Go durations plus a day suffix ("7d", "1d12h").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var total time.Duration
	rest := s
	for {
		i := strings.IndexByte(rest, 'd')
		if i < 0 {
			break
		}
		// A 'd' is only a day marker when followed by nothing or a digit.
		if i+1 < len(rest) && (rest[i+1] < '0' || rest[i+1] > '9') {
			break
		}
		days, err := strconv.Atoi(rest[:i])
		if err != nil {
			return 0, fmt.Errorf("cannot parse duration %q", s)
		}
		total += time.Duration(days) * 24 * time.Hour
		rest = rest[i+1:]
	}
	if rest != "" {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("cannot parse duration %q", s)
		}
		total += d
	}
	if total == 0 {
		return 0, fmt.Errorf("cannot parse duration %q", s)
	}
	return total, nil
}

// Save writes cfg to path as YAML.
func Save(path string, cfg *Config) error {
	cfg.Version = CurrentVersion
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// Keys lists dotted configuration keys that `orbit config set` understands.
func Keys() []string {
	out := []string{
		"language",
		"defaults.platforms",
		"defaults.products",
		"defaults.strategy",
		"defaults.concurrency.products",
		"defaults.concurrency.changes",
		"defaults.timeouts.api",
		"defaults.timeouts.deploy_poll",
		"defaults.cert_preference",
		"defaults.domain_match.wildcard",
		"defaults.domain_match.allow_expired",
		"defaults.experimental_write",
		"defaults.edgeone.replace_free_cert",
		"defaults.clean.older_than",
		"defaults.clean.require_full_evidence",
		"defaults.clean.include_free",
		"defaults.clean.protect_patterns",
	}
	sort.Strings(out)
	return out
}

func readMap(path string) (map[string]any, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, false, nil
		}
		return nil, false, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, true, nil
}

// merge returns dst overridden by src (src wins), recursively for nested maps.
func merge(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if cur, ok := dst[k].(map[string]any); ok {
				dst[k] = merge(cur, sub)
				continue
			}
			dst[k] = merge(map[string]any{}, sub)
			continue
		}
		dst[k] = v
	}
	return dst
}

func applyDefaults(m map[string]any) {
	setDefault(m, []string{"version"}, CurrentVersion)
	setDefault(m, []string{"language"}, "")
	setDefault(m, []string{"defaults", "platforms"}, []string{"tencent"})
	setDefault(m, []string{"defaults", "products"}, SupportedProducts)
	setDefault(m, []string{"defaults", "strategy"}, "auto")
	setDefault(m, []string{"defaults", "concurrency", "products"}, 4)
	setDefault(m, []string{"defaults", "concurrency", "changes"}, 2)
	setDefault(m, []string{"defaults", "timeouts", "api"}, "15s")
	setDefault(m, []string{"defaults", "timeouts", "deploy_poll"}, "5m")
	setDefault(m, []string{"defaults", "cert_preference"}, []string{"upload", "buy"})
	setDefault(m, []string{"defaults", "domain_match", "wildcard"}, true)
	setDefault(m, []string{"defaults", "domain_match", "allow_expired"}, false)
	setDefault(m, []string{"defaults", "experimental_write"}, false)
	setDefault(m, []string{"defaults", "edgeone", "replace_free_cert"}, true)
	setDefault(m, []string{"defaults", "clean", "older_than"}, "7d")
	setDefault(m, []string{"defaults", "clean", "require_full_evidence"}, true)
	setDefault(m, []string{"defaults", "clean", "include_free"}, false)
	setDefault(m, []string{"defaults", "clean", "protect_patterns"}, []string{})
}

func setDefault(m map[string]any, path []string, value any) {
	cur := m
	for i, key := range path {
		if i == len(path)-1 {
			if _, ok := cur[key]; !ok {
				cur[key] = value
			}
			return
		}
		next, ok := cur[key]
		if !ok {
			child := map[string]any{}
			cur[key] = child
			cur = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return
		}
		cur = child
	}
}
