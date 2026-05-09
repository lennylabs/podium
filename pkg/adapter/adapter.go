// Package adapter defines the HarnessAdapter SPI (spec §6.7) and ships the
// none adapter, which writes the canonical artifact layout as-is.
//
// HarnessAdapter implementations translate canonical artifacts into the
// harness-native layout at materialization time. Adapters MUST NOT make
// network calls, MUST NOT spawn subprocesses, and MUST NOT write outside
// the materialization destination (§6.7 sandbox contract).
package adapter

import (
	"fmt"
	"strings"
)

// File is one output file produced by an adapter. Path is relative to the
// destination root; Mode defaults to 0o644 when zero.
type File struct {
	Path    string
	Content []byte
	Mode    uint32
}

// Source is the canonical input given to an adapter. It bundles the
// artifact identity, manifest sources, and bundled-resource bytes.
type Source struct {
	// ArtifactID is the canonical artifact path under the registry root,
	// e.g., "finance/ap/pay-invoice".
	ArtifactID string
	// ArtifactBytes is the verbatim bytes of ARTIFACT.md.
	ArtifactBytes []byte
	// SkillBytes is the verbatim bytes of SKILL.md (only for type: skill).
	SkillBytes []byte
	// Resources are bundled non-manifest files keyed by relative path
	// inside the artifact directory (e.g., "scripts/x.py").
	Resources map[string][]byte
}

// HarnessAdapter is the SPI implementations satisfy.
type HarnessAdapter interface {
	// ID returns the adapter identifier (e.g., "none", "claude-code").
	// Identifiers match the PODIUM_HARNESS env values per §6.7.
	ID() string

	// Adapt produces the harness-native output for src. Implementations
	// must not perform IO; the returned files are written by
	// pkg/materialize under the sandbox contract.
	Adapt(src Source) ([]File, error)
}

// Registry holds the set of HarnessAdapter implementations registered by
// the binary. The default registry exposes the built-ins; tests construct
// their own to swap mocks in.
type Registry struct {
	byID map[string]HarnessAdapter
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]HarnessAdapter{}}
}

// Register adds the adapter under its ID. Returns an error when a duplicate
// ID is registered.
func (r *Registry) Register(a HarnessAdapter) error {
	if _, ok := r.byID[a.ID()]; ok {
		return fmt.Errorf("adapter %q already registered", a.ID())
	}
	r.byID[a.ID()] = a
	return nil
}

// Get returns the registered adapter for id, or an error when no adapter
// claims that ID. Maps to the §6.10 namespace (config.unknown_harness).
func (r *Registry) Get(id string) (HarnessAdapter, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("config.unknown_harness: no adapter registered for %q (have: %s)",
			id, strings.Join(r.IDs(), ", "))
	}
	return a, nil
}

// IDs returns the registered adapter IDs in alphabetical order.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	// Sort lexicographically.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// DefaultRegistry returns a Registry pre-populated with the built-in
// adapters available in the active phase. Phase 0 ships only "none";
// Phase 3 adds claude-code and codex; Phase 13 adds the remaining built-ins.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	_ = r.Register(None{})
	return r
}
