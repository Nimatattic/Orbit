// Package locales embeds the message catalogs shipped with the binary.
//
// Catalogs are flat JSON objects mapping a stable message key to a template
// containing {name} placeholders. Keeping them embedded makes the CLI a single
// self-contained file, which matters for cron/1Panel deployments.
package locales

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// FS holds every catalog file. It is exported so tests can walk it.
//
//go:embed *.json
var FS embed.FS

// Supported returns the catalog identifiers (e.g. "zh-CN"), sorted.
func Supported() []string {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

// Catalog loads and decodes the catalog for lang.
func Catalog(lang string) (map[string]string, error) {
	raw, err := FS.ReadFile(path.Join(".", lang+".json"))
	if err != nil {
		return nil, fmt.Errorf("locale %q not found: %w", lang, err)
	}
	msgs := map[string]string{}
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, fmt.Errorf("locale %q is not valid JSON: %w", lang, err)
	}
	return msgs, nil
}

// Keys returns the sorted message keys of one catalog.
func Keys(lang string) ([]string, error) {
	msgs, err := Catalog(lang)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(msgs))
	for k := range msgs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
