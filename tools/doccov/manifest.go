package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest is the parsed doccov manifest: the mapping from each runnable doc
// page to its covering test slug or its waiver.
type Manifest struct {
	Pages []Entry `yaml:"pages"`
}

// Entry is one manifest row. Exactly one of Slug and Waiver is set: Slug names
// the D-<slug> of the covering end-to-end test, Waiver gives the reason the
// page carries no independent test.
type Entry struct {
	Path   string `yaml:"path"`
	Slug   string `yaml:"slug"`
	Waiver string `yaml:"waiver"`
}

// LoadManifest reads and validates the manifest at path. It rejects a row that
// sets neither slug nor waiver, sets both, omits its path, or duplicates an
// earlier path, so the manifest cannot silently leave a page ambiguous.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := map[string]bool{}
	for i := range m.Pages {
		e := &m.Pages[i]
		e.Path = normPath(e.Path)
		if e.Path == "" {
			return nil, fmt.Errorf("%s: manifest entry %d has an empty path", path, i)
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("%s: duplicate manifest entry for %s", path, e.Path)
		}
		seen[e.Path] = true
		hasSlug := e.Slug != ""
		hasWaiver := e.Waiver != ""
		if hasSlug == hasWaiver {
			return nil, fmt.Errorf("%s: entry for %s must set exactly one of slug or waiver", path, e.Path)
		}
	}
	return &m, nil
}

// byPath indexes the entries by their normalized doc path.
func (m *Manifest) byPath() map[string]Entry {
	out := make(map[string]Entry, len(m.Pages))
	for _, e := range m.Pages {
		out[e.Path] = e
	}
	return out
}
