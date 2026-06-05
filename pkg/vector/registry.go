package vector

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the process-global registration seam for the §9.1
// RegistrySearchProvider SPI, distributed per §9.2 as an in-process Go
// module imported into a registry build. §9.1 states custom backends
// "register through this SPI as Go-module plugins (§9.2)"; this is the
// seam that makes the claim hold. A deployment imports a package whose
// init calls Default.Register, and the bootstrap selects the backend by
// the PODIUM_VECTOR_BACKEND id, consulting this registry before the
// built-in switch.
//
// Settings is a wire-serializable map of resolved configuration values so
// a future out-of-process provider (§9.3) receives the same inputs.
// Dimensions is the embedding dimension the selected EmbeddingProvider
// emits (0 when the backend self-embeds).
//
// spec: §9.1 (RegistrySearchProvider), §9.2 (Go-module plugins).
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// Factory constructs a Provider from the resolved settings and the
// embedding dimension. Returning an error fails startup rather than
// silently disabling search.
type Factory func(settings map[string]string, dimensions int) (Provider, error)

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Default is the process-global vector-backend registry the bootstrap
// consults before its built-in switch. It is empty by default; the
// built-in backends remain the switch's concern so their per-backend
// env-var validation is unchanged. Deployers add custom backends via
// Default.Register.
var Default = NewRegistry()

// Register adds a Factory under id. Returns an error when id is empty or
// already registered.
func (r *Registry) Register(id string, f Factory) error {
	if id == "" {
		return errors.New("vector: empty backend id")
	}
	if f == nil {
		return errors.New("vector: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[id]; ok {
		return fmt.Errorf("vector: backend %q already registered", id)
	}
	r.factories[id] = f
	return nil
}

// New constructs the backend registered under id. Returns
// (nil, false, nil) when no backend is registered so the caller can fall
// through to the built-in switch.
func (r *Registry) New(id string, settings map[string]string, dimensions int) (Provider, bool, error) {
	r.mu.RLock()
	f, ok := r.factories[id]
	r.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	p, err := f(settings, dimensions)
	return p, true, err
}

// IDs returns every registered backend id, sorted.
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
