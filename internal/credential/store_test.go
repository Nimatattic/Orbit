package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Crequency/orbit/internal/config"
)

func TestStoreRoundTripUses0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.yaml")
	store := NewStore(path)

	if err := store.Set("main", "AKIDEXAMPLE", "SECRETEXAMPLE"); err != nil {
		t.Fatalf("set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %04o, want 0600 (decision B1)", got)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Accounts["main"].SecretID != "AKIDEXAMPLE" {
		t.Fatalf("round trip lost the secret id: %+v", loaded)
	}

	aliases, err := store.Aliases()
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0] != "main" {
		t.Fatalf("aliases = %v", aliases)
	}

	removed, err := store.Remove("main")
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
}

func TestLoadRejectsPermissiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(path, []byte("version: 1\naccounts:\n  main:\n    secret_id: a\n    secret_key: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(path).Load()
	if err == nil {
		t.Fatal("a 0644 credential file must be refused")
	}
	var me *ModeError
	if !errors.As(err, &me) {
		t.Fatalf("got %T (%v), want *ModeError", err, err)
	}
	if !IsModeError(err) {
		t.Fatal("IsModeError should report true")
	}
}

func TestResolvePrefersEnvironment(t *testing.T) {
	t.Setenv("MY_SECRET_ID", "ENVID")
	t.Setenv("MY_SECRET_KEY", "ENVKEY")
	ref := config.CredentialRef{Source: "env", SecretIDEnv: "MY_SECRET_ID", SecretKeyEnv: "MY_SECRET_KEY"}

	got, err := Resolve("main", ref, &Credentials{Accounts: map[string]Account{
		"main": {SecretID: "STOREDID", SecretKey: "STOREDKEY"},
	}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "ENVID" || got.SecretKey != "ENVKEY" {
		t.Fatalf("environment must win, got %+v", got)
	}
}

func TestResolveFallsBackToStore(t *testing.T) {
	got, err := Resolve("main", config.CredentialRef{Source: "store"}, &Credentials{Accounts: map[string]Account{
		"main": {SecretID: "STOREDID", SecretKey: "STOREDKEY"},
	}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "STOREDID" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveMissingAccount(t *testing.T) {
	if _, err := Resolve("nope", config.CredentialRef{}, &Credentials{}); err == nil {
		t.Fatal("expected an error for an unregistered account")
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("AKIDabcdef"); got != "AKID***" {
		t.Fatalf("got %q", got)
	}
	if got := Redact("abc"); got != "***" {
		t.Fatalf("short secrets must be fully masked, got %q", got)
	}
	if got := Redact(""); got != "" {
		t.Fatalf("empty secret must stay empty, got %q", got)
	}
}
