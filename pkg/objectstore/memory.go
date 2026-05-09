package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

// Memory is an in-memory Provider used by tests and by the conformance
// suite. Production deployments select Filesystem or S3.
type Memory struct {
	mu      sync.Mutex
	objects map[string]memoryObject
	// BaseURL prefixes URLs returned by Presign so tests can confirm
	// the expected shape.
	BaseURL string
}

type memoryObject struct {
	body        []byte
	contentType string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{objects: map[string]memoryObject{}}
}

// ID returns "memory".
func (m *Memory) ID() string { return "memory" }

// Put stores body under key.
func (m *Memory) Put(_ context.Context, key string, body []byte, contentType string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.objects[key]; ok && !bytes.Equal(existing.body, body) {
		return fmt.Errorf("objectstore.memory: key %q already exists with different body", key)
	}
	m.objects[key] = memoryObject{body: append([]byte(nil), body...), contentType: contentType}
	return nil
}

// Get returns the body for key.
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), o.body...), nil
}

// Presign returns a deterministic URL of the form <BaseURL>/<key>.
// Memory has no TTL story; ttl is preserved for parity with the SPI.
func (m *Memory) Presign(_ context.Context, key string, _ time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	base := m.BaseURL
	if base == "" {
		base = "memory://"
	}
	return base + "/" + key, nil
}

// Delete removes the object. Missing key is a no-op.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// validateKey rejects empty or path-traversing keys. Backends call
// this from Put / Presign to surface the same ErrInvalidKey shape.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	}
	if containsTraversal(key) {
		return fmt.Errorf("%w: contains '..'", ErrInvalidKey)
	}
	return nil
}

func containsTraversal(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}
