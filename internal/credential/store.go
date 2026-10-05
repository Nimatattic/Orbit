// Package credential stores and resolves Tencent Cloud API keys.
//
// Decision A1: Orbit never reads ~/.tccli/*.credential. Keys are entered by the
// user, stored under ~/.orbit/credentials.yaml with mode 0600 (decision B1), and
// may be overridden per-run through environment variables for cron usage.
package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Crequency/orbit/internal/config"
)

// Account is one stored key pair.
type Account struct {
	SecretID  string `yaml:"secret_id"`
	SecretKey string `yaml:"secret_key"`
}

// Credentials is the on-disk document.
type Credentials struct {
	Version  int                `yaml:"version"`
	Accounts map[string]Account `yaml:"accounts"`
}

// ModeError reports a credential file that is readable by other users.
type ModeError struct {
	Path string
	Mode os.FileMode
}

func (e *ModeError) Error() string {
	return fmt.Sprintf("credential file %s has mode %04o, expected 0600", e.Path, e.Mode.Perm())
}

// IsModeError reports whether err is a permission complaint.
func IsModeError(err error) bool {
	var e *ModeError
	return errors.As(err, &e)
}

// Store persists credentials at a fixed path.
type Store struct{ path string }

// NewStore returns a store for path (see config.CredentialsPath).
func NewStore(path string) *Store { return &Store{path: path} }

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Load reads credentials, refusing files that are group/world accessible.
func (s *Store) Load() (*Credentials, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Credentials{Version: 1, Accounts: map[string]Account{}}, nil
		}
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, &ModeError{Path: s.path, Mode: info.Mode()}
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	out := &Credentials{}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	if out.Accounts == nil {
		out.Accounts = map[string]Account{}
	}
	if out.Version == 0 {
		out.Version = 1
	}
	return out, nil
}

// Save writes credentials atomically with mode 0600.
func (s *Store) Save(c *Credentials) error {
	if c == nil {
		return errors.New("credential: nil document")
	}
	c.Version = 1
	if c.Accounts == nil {
		c.Accounts = map[string]Account{}
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Set stores or replaces one account's key pair.
func (s *Store) Set(alias, secretID, secretKey string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return errors.New("credential: alias is required")
	}
	creds, err := s.Load()
	if err != nil && !os.IsNotExist(err) {
		if !IsModeError(err) {
			return err
		}
		// A permissive file still needs fixing before we write anything new.
		return err
	}
	creds.Accounts[alias] = Account{SecretID: secretID, SecretKey: secretKey}
	return s.Save(creds)
}

// Remove deletes one account. It reports whether the alias existed.
func (s *Store) Remove(alias string) (bool, error) {
	creds, err := s.Load()
	if err != nil {
		return false, err
	}
	if _, ok := creds.Accounts[alias]; !ok {
		return false, nil
	}
	delete(creds.Accounts, alias)
	return true, s.Save(creds)
}

// Aliases lists stored account aliases in sorted order.
func (s *Store) Aliases() ([]string, error) {
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(creds.Accounts))
	for alias := range creds.Accounts {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out, nil
}

// Resolve returns the effective key pair for an account. Explicit environment
// variables win over the stored file, which is what cron/1Panel uses.
func Resolve(alias string, ref config.CredentialRef, creds *Credentials) (Account, error) {
	if id := firstEnv(ref.SecretIDEnv, envName(alias, "SECRET_ID")); id != "" {
		key := firstEnv(ref.SecretKeyEnv, envName(alias, "SECRET_KEY"))
		if key == "" {
			return Account{}, fmt.Errorf("credential: %s is set but the matching secret key is not", id)
		}
		return Account{SecretID: id, SecretKey: key}, nil
	}
	if creds != nil {
		if acc, ok := creds.Accounts[alias]; ok && acc.SecretID != "" {
			return acc, nil
		}
	}
	return Account{}, fmt.Errorf("credential: no credentials for account %q", alias)
}

// Redact masks a secret for display, keeping a short prefix.
func Redact(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "***"
	}
	return secret[:4] + "***"
}

func envName(alias, suffix string) string {
	up := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(alias))
	return "ORBIT_TENCENT_" + up + "_" + suffix
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if n == "" {
			continue
		}
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}
