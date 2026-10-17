package i18n

import (
	"strings"
	"testing"

	"github.com/Crequency/orbit/locales"
)

// TestCatalogsShareTheSameKeySet guards against a message that exists in one
// language only, which would show up as a raw key at runtime.
func TestCatalogsShareTheSameKeySet(t *testing.T) {
	langs := locales.Supported()
	if len(langs) < 2 {
		t.Fatalf("expected at least two catalogs, got %v", langs)
	}
	base, err := locales.Catalog(langs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range langs[1:] {
		other, err := locales.Catalog(lang)
		if err != nil {
			t.Fatal(err)
		}
		for key := range base {
			if _, ok := other[key]; !ok {
				t.Errorf("%s is missing key %q", lang, key)
			}
		}
		for key := range other {
			if _, ok := base[key]; !ok {
				t.Errorf("%s has extra key %q", lang, key)
			}
		}
		for key, value := range other {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s: key %q has an empty message", lang, key)
			}
		}
	}
}

func TestNegotiate(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "en-US", want: "en-US"},
		{in: "en_US.UTF-8", want: "en-US"},
		{in: "zh-CN", want: "zh-CN"},
		{in: "zh_CN.UTF-8", want: "zh-CN"},
		{in: "de-DE", want: Fallback},
		{in: "", want: Fallback},
	}
	for _, tc := range tests {
		if got := Negotiate(tc.in); got != tc.want {
			t.Errorf("Negotiate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubstitutionAndMissingKeys(t *testing.T) {
	b, err := New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	got := b.T("plan.summary", "total", 4, "apply", 2, "uptodate", 1, "nocert", 1)
	if strings.Contains(got, "{") {
		t.Fatalf("placeholders were not substituted: %q", got)
	}
	if !strings.Contains(got, "4") {
		t.Fatalf("expected the count in %q", got)
	}
	if out := b.T("no.such.key"); out != "no.such.key" {
		t.Fatalf("unknown keys must render as-is, got %q", out)
	}
	if len(b.Missing()) == 0 {
		t.Fatal("unknown keys must be recorded by Missing()")
	}
}

func TestFromEnvPrecedence(t *testing.T) {
	t.Setenv("ORBIT_LANG", "en-US")
	if got := FromEnv("zh-CN", "zh-CN"); got != "zh-CN" {
		t.Fatalf("explicit language must win, got %q", got)
	}
	if got := FromEnv("", "zh-CN"); got != "en-US" {
		t.Fatalf("ORBIT_LANG must beat the configured language, got %q", got)
	}
	t.Setenv("ORBIT_LANG", "")
	if got := FromEnv("", "en-US"); got != "en-US" {
		t.Fatalf("configured language should apply, got %q", got)
	}
}

func TestFromEnvIgnoresNeutralPosixLocales(t *testing.T) {
	// LC_ALL=C is what cron and systemd hand us; it means "no preference", so the
	// decision must fall through to LANG instead of forcing the fallback language.
	t.Setenv("ORBIT_LANG", "")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := FromEnv("", ""); got != "en-US" {
		t.Fatalf("FromEnv = %q, want en-US (LC_ALL=C must not pin the language)", got)
	}

	// An explicit request still wins over everything.
	if got := FromEnv("zh-CN", "en-US"); got != "zh-CN" {
		t.Fatalf("explicit --lang must win, got %q", got)
	}

	// With no usable preference at all we keep the documented fallback.
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "POSIX")
	if got := FromEnv("", ""); got != Fallback {
		t.Fatalf("FromEnv = %q, want %q", got, Fallback)
	}
}
