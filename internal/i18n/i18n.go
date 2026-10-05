// Package i18n resolves a user language and renders message keys.
//
// Design rule (plan §4.1): core logic only ever produces message KEYS plus
// arguments. Translation happens in the presentation layer, so business code
// never grows language branches.
package i18n

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/text/language"

	"github.com/Crequency/orbit/locales"
)

// Fallback is used when nothing else matches (decision B5).
const Fallback = "zh-CN"

// Bundle renders messages for one resolved language.
type Bundle struct {
	lang     string
	msgs     map[string]string
	fallback map[string]string
	missing  map[string]struct{}
}

// Supported reports the languages compiled into this binary.
func Supported() []string {
	langs := locales.Supported()
	if len(langs) == 0 {
		return []string{Fallback}
	}
	return langs
}

// Negotiate maps a user preference (--lang, ORBIT_LANG, config, $LANG) to one of
// the supported languages, falling back to Fallback.
func Negotiate(prefer string) string {
	supported := Supported()
	tags := make([]language.Tag, 0, len(supported))
	for _, s := range supported {
		if t, err := language.Parse(s); err == nil {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 {
		return Fallback
	}
	matcher := language.NewMatcher(tags)
	prefer = normaliseLocale(prefer)
	if prefer == "" {
		return Fallback
	}
	tag, err := language.Parse(prefer)
	if err != nil {
		return Fallback
	}
	_, idx, conf := matcher.Match(tag)
	if idx < 0 || conf == language.No {
		return Fallback
	}
	return supported[idx]
}

// FromEnv implements the documented precedence: explicit > ORBIT_LANG > $LANG.
func FromEnv(explicit, fromConfig string) string {
	for _, candidate := range []string{explicit, os.Getenv("ORBIT_LANG"), fromConfig, os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if strings.TrimSpace(candidate) != "" {
			return Negotiate(candidate)
		}
	}
	return Fallback
}

// New builds a bundle for prefer, always keeping the fallback catalog at hand.
func New(prefer string) (*Bundle, error) {
	lang := Negotiate(prefer)
	msgs, err := locales.Catalog(lang)
	if err != nil {
		return nil, err
	}
	fb, err := locales.Catalog(Fallback)
	if err != nil {
		return nil, err
	}
	return &Bundle{lang: lang, msgs: msgs, fallback: fb, missing: map[string]struct{}{}}, nil
}

// Lang returns the resolved language identifier.
func (b *Bundle) Lang() string { return b.lang }

// T renders key with the given key/value argument pairs, e.g.
// T("account.added", "alias", "main"). Unknown keys fall back to the fallback
// catalog and are recorded by Missing.
func (b *Bundle) T(key string, kv ...any) string {
	if b == nil {
		return key
	}
	tmpl, ok := b.msgs[key]
	if !ok {
		if tmpl, ok = b.fallback[key]; ok {
			b.missing[key] = struct{}{}
		} else {
			b.missing[key] = struct{}{}
			return key
		}
	}
	repl := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		name := fmt.Sprint(kv[i])
		repl = append(repl, "{"+name+"}", fmt.Sprint(kv[i+1]))
	}
	if len(repl) > 0 {
		tmpl = strings.NewReplacer(repl...).Replace(tmpl)
	}
	return tmpl
}

// Missing lists keys that were requested but absent (used by doctor and tests).
func (b *Bundle) Missing() []string {
	out := make([]string, 0, len(b.missing))
	for k := range b.missing {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normaliseLocale(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '.'); i >= 0 { // strip ".UTF-8"
		s = s[:i]
	}
	if i := strings.IndexByte(s, '@'); i >= 0 { // strip "@euro"
		s = s[:i]
	}
	return strings.ReplaceAll(s, "_", "-")
}
