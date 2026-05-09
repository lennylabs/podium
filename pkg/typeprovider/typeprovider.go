// Package typeprovider implements the §9 TypeProvider SPI. Each
// first-class artifact type (skill, agent, context, command, rule,
// hook, mcp-server) ships as a TypeProvider; deployers register
// extension types through the SPI to add validators, lint rules,
// or harness-adapter outputs without forking pkg/manifest.
//
// The registry is a process-global singleton keyed by ArtifactType.
// Register-time conflicts return an error so deployments fail loud
// when two providers claim the same type id.
package typeprovider

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Diagnostic mirrors a single lint diagnostic. Defined locally so
// pkg/typeprovider does not import pkg/lint (which already depends
// on pkg/manifest, which would otherwise cycle).
type Diagnostic struct {
	Severity string // "error" | "warn" | "info"
	Code     string
	Message  string
	Path     string
}

// Provider is the SPI an artifact-type implementation satisfies.
//
// Type returns the canonical ArtifactType (e.g. "skill",
// "macro"). Validate runs the type-specific structural checks
// against an already-parsed manifest.Artifact and returns any
// diagnostics the linter should surface.
type Provider interface {
	ID() string
	Type() manifest.ArtifactType
	Validate(*manifest.Artifact) []Diagnostic
}

// Registry holds the registered providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[manifest.ArtifactType]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[manifest.ArtifactType]Provider{}}
}

// Default is the process-global registry the rest of Podium reads
// from when validating manifests. New() seeds it with the
// first-class types as no-op validators; deployers extend via
// Default.Register.
var Default = newDefault()

func newDefault() *Registry {
	r := NewRegistry()
	for _, t := range []manifest.ArtifactType{
		manifest.TypeSkill,
		manifest.TypeAgent,
		manifest.TypeContext,
		manifest.TypeCommand,
		manifest.TypeRule,
		manifest.TypeHook,
		manifest.TypeMCPServer,
	} {
		_ = r.Register(builtin{typ: t})
	}
	return r
}

// Register adds p to the registry. Returns an error when a
// provider for the same type is already registered.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return errors.New("typeprovider: nil provider")
	}
	if p.Type() == "" {
		return errors.New("typeprovider: provider has empty type")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.providers[p.Type()]; ok {
		return fmt.Errorf("typeprovider: type %q already registered by %q",
			p.Type(), existing.ID())
	}
	r.providers[p.Type()] = p
	return nil
}

// Get returns the provider for typ, or false when unregistered.
func (r *Registry) Get(typ manifest.ArtifactType) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[typ]
	return p, ok
}

// Types returns every registered type id, sorted.
func (r *Registry) Types() []manifest.ArtifactType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]manifest.ArtifactType, 0, len(r.providers))
	for t := range r.providers {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Validate dispatches to the registered provider's Validate, or
// returns nil when no provider matches a (the linter's other rules
// catch the unknown-type case).
func (r *Registry) Validate(a *manifest.Artifact) []Diagnostic {
	if a == nil {
		return nil
	}
	r.mu.RLock()
	p, ok := r.providers[a.Type]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return p.Validate(a)
}

// builtin is the default TypeProvider for every first-class type.
// It does not run additional validation; the existing pkg/lint
// rules continue to handle universal-field checks.
type builtin struct {
	typ manifest.ArtifactType
}

func (b builtin) ID() string                                  { return "builtin:" + string(b.typ) }
func (b builtin) Type() manifest.ArtifactType                 { return b.typ }
func (b builtin) Validate(*manifest.Artifact) []Diagnostic    { return nil }
