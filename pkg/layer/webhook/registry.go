package webhook

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the process-global registration seam for the §9.1
// GitProvider SPI, distributed per §9.2 as an in-process Go module
// imported into a registry build. A deployment that needs a custom
// GitProvider (a self-hosted forge, a non-standard signature scheme)
// imports a package whose init calls Default.Register, and the inbound
// webhook verification path selects the provider by id. This mirrors the
// TypeProvider seam (pkg/typeprovider) so every SPI §9.2 names by example
// shares the same compile-time, in-process registration mechanism.
//
// spec: §9.1 (GitProvider), §9.2 (plugins ship as Go modules imported
// into a registry build), §7.3.1 (inbound webhook signature verification).
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Default is the process-global GitProvider registry the inbound webhook
// path consults to verify a delivery's signature. It is seeded with the
// built-in github, gitlab, and bitbucket providers; deployers add custom
// providers via Default.Register.
var Default = newDefault()

func newDefault() *Registry {
	r := NewRegistry()
	_ = r.Register(GitHub{})
	_ = r.Register(GitLab{})
	_ = r.Register(Bitbucket{})
	return r
}

// Register adds p under p.ID(). Returns an error when the id is empty or
// already registered so two providers cannot silently claim the same id.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return errors.New("webhook: nil provider")
	}
	id := p.ID()
	if id == "" {
		return errors.New("webhook: provider has empty id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[id]; ok {
		return fmt.Errorf("webhook: provider %q already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider registered under id, or false when none is.
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

// Verify selects the provider registered under id and checks the
// signature. Returns ErrUnknownProvider when no provider matches id, so a
// delivery from an unconfigured forge is rejected rather than ingested.
func (r *Registry) Verify(id string, body []byte, signature, secret string) error {
	p, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownProvider, id)
	}
	return p.Verify(body, signature, secret)
}

// IDs returns every registered provider id, sorted.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownProvider signals a webhook delivery naming a provider id with
// no registered GitProvider. Maps to ingest.webhook_invalid in §6.10.
var ErrUnknownProvider = errors.New("webhook: unknown_provider")
