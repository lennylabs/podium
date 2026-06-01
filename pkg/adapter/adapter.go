// Package adapter defines the HarnessAdapter SPI (spec §6.7) and ships the
// none adapter, which writes the canonical artifact layout as-is.
//
// HarnessAdapter implementations translate canonical artifacts into the
// harness-native layout at materialization time. Adapters MUST NOT make
// network calls, MUST NOT spawn subprocesses, and MUST NOT write outside
// the materialization destination (§6.7 sandbox contract).
package adapter

import (
	"context"
	"fmt"
	"strings"
)

// FileOp selects how the materializer applies a File to its destination.
type FileOp int

const (
	// OpWrite writes Content as a standalone file, replacing any prior
	// content. This is the default (zero value) and covers skill folders,
	// agent/command/rule files, and bundled resources.
	OpWrite FileOp = iota
	// OpInject merges Content into a shared text file (markdown or TOML)
	// between Podium-managed markers keyed by Key, so the operator's other
	// content in the file is preserved and a re-sync reconciles only Podium's
	// block. Used for rules injected into AGENTS.md / GEMINI.md and for
	// config.toml tables.
	OpInject
	// OpMergeJSON deep-merges Content (a JSON object) into the JSON file at
	// Path under Podium-owned keys, preserving the operator's other keys.
	// Used for hook and mcp-server config (settings.json, .mcp.json,
	// .cursor/*.json, opencode.json, .codex/hooks.json).
	OpMergeJSON
)

// PodiumOwnedKey tags a config-merge entry as Podium-owned, keyed to the
// artifact ID (§6.7 "a Podium-owned entry keyed by the artifact ID", carried
// in the §6.7 x-podium-* extension namespace). Each config-merge fragment
// stamps its entry with this key so the materialize layer can rebuild Podium's
// contribution on every sync: prior Podium entries are stripped before the
// current set is merged, which preserves the operator's untagged entries,
// accumulates multiple Podium entries, and removes an artifact's entry once it
// is gone.
const PodiumOwnedKey = "x-podium-id"

// File is one output produced by an adapter. Path is relative to the
// destination root; Mode defaults to 0o644 when zero. Op selects the apply
// mode (default OpWrite); Key is the artifact ID that scopes a Podium-managed
// inject block.
type File struct {
	Path    string
	Content []byte
	Mode    uint32
	Op      FileOp
	Key     string
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
	Adapt(ctx context.Context, src Source) ([]File, error)
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
// adapters per §6.7.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	for _, a := range []HarnessAdapter{
		None{},
		ClaudeCode{},
		ClaudeDesktop{},
		ClaudeCowork{},
		Cursor{},
		Codex{},
		Gemini{},
		OpenCode{},
		Pi{},
		Hermes{},
	} {
		_ = r.Register(a)
	}
	return r
}
