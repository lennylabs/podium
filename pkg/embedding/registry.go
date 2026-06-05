package embedding

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the process-global registration seam for the §9.1
// EmbeddingProvider SPI, distributed per §9.2 as an in-process Go module
// imported into a registry build. §9.1 states custom backends "register
// through this SPI as Go-module plugins (§9.2)"; this is the seam that
// makes the claim hold. A deployment imports a package whose init calls
// Default.Register, and the bootstrap selects the provider by the
// PODIUM_EMBEDDING_PROVIDER id, consulting this registry before the
// built-in switch.
//
// Settings is a wire-serializable map of resolved configuration values so
// a future out-of-process provider (§9.3) receives the same inputs.
//
// spec: §9.1 (EmbeddingProvider), §9.2 (Go-module plugins).
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// Factory constructs a Provider from the resolved settings. Returning an
// error fails startup rather than silently disabling search.
type Factory func(settings map[string]string) (Provider, error)

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Default is the process-global embedding-provider registry the bootstrap
// consults before its built-in switch. It is empty by default; the
// built-in providers (openai, voyage, cohere, ollama) remain the switch's
// concern so their per-provider env-var validation is unchanged. Deployers
// add custom providers via Default.Register.
var Default = NewRegistry()

// Register adds a Factory under id. Returns an error when id is empty or
// already registered.
func (r *Registry) Register(id string, f Factory) error {
	if id == "" {
		return errors.New("embedding: empty provider id")
	}
	if f == nil {
		return errors.New("embedding: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[id]; ok {
		return fmt.Errorf("embedding: provider %q already registered", id)
	}
	r.factories[id] = f
	return nil
}

// New constructs the provider registered under id. Returns
// (nil, false, nil) when no provider is registered so the caller can fall
// through to the built-in switch.
func (r *Registry) New(id string, settings map[string]string) (Provider, bool, error) {
	r.mu.RLock()
	f, ok := r.factories[id]
	r.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	p, err := f(settings)
	return p, true, err
}

// IDs returns every registered provider id, sorted.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for id := range r.factories {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
