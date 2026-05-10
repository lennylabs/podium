package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FilePersistedRuntimeKeyRegistry is a RuntimeKeyRegistry that
// persists registrations to a JSON file. Loaded keys are
// deserialized via x509.ParsePKIXPublicKey at construction time,
// then handed to the embedded in-memory registry. Mutations write
// the file back atomically.
//
// Suitable for §6.3.2 deployments where runtime trust keys are
// rare (~10s) and infrequently rotated. Heavier deployments
// should use a SQL-backed store.
type FilePersistedRuntimeKeyRegistry struct {
	*RuntimeKeyRegistry
	path string
	mu   sync.Mutex
}

// LoadFilePersistedRuntimeKeyRegistry reads path (when present)
// and returns a registry pre-populated with every record. Missing
// path yields an empty registry that will create the file on the
// first Register.
func LoadFilePersistedRuntimeKeyRegistry(path string) (*FilePersistedRuntimeKeyRegistry, error) {
	if path == "" {
		return nil, errors.New("runtime: path required")
	}
	out := &FilePersistedRuntimeKeyRegistry{
		RuntimeKeyRegistry: NewRuntimeKeyRegistry(),
		path:               path,
	}
	if err := out.load(); err != nil {
		return nil, err
	}
	return out, nil
}

// runtimeKeyJSON is the on-disk shape: the public key is encoded
// as a PKIX PEM block so the file is human-readable and the
// algorithm field guides type assertion at load time.
type runtimeKeyJSON struct {
	Issuer       string `json:"issuer"`
	Algorithm    string `json:"algorithm"`
	PublicKeyPEM string `json:"public_key_pem"`
}

func (r *FilePersistedRuntimeKeyRegistry) load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("runtime: read %s: %w", r.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var raw []runtimeKeyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("runtime: parse %s: %w", r.path, err)
	}
	for _, k := range raw {
		pub, err := ParsePublicKeyPEM(k.PublicKeyPEM, k.Algorithm)
		if err != nil {
			return fmt.Errorf("runtime: load %q: %w", k.Issuer, err)
		}
		if err := r.RuntimeKeyRegistry.Register(RuntimeKey{
			Issuer:    k.Issuer,
			Algorithm: k.Algorithm,
			Key:       pub,
		}); err != nil {
			return fmt.Errorf("runtime: register %q: %w", k.Issuer, err)
		}
	}
	return nil
}

// Register adds rk and persists the file atomically.
func (r *FilePersistedRuntimeKeyRegistry) Register(rk RuntimeKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.RuntimeKeyRegistry.Register(rk); err != nil {
		return err
	}
	return r.save()
}

func (r *FilePersistedRuntimeKeyRegistry) save() error {
	all := r.RuntimeKeyRegistry.All()
	out := make([]runtimeKeyJSON, 0, len(all))
	for _, k := range all {
		pemBytes, err := encodePublicKey(k.Key)
		if err != nil {
			return fmt.Errorf("runtime: encode %q: %w", k.Issuer, err)
		}
		out = append(out, runtimeKeyJSON{
			Issuer:       k.Issuer,
			Algorithm:    k.Algorithm,
			PublicKeyPEM: string(pemBytes),
		})
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// encodePublicKey returns a PEM-encoded PKIX public key for any
// type ParsePublicKeyPEM accepts.
func encodePublicKey(key any) ([]byte, error) {
	switch key.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
	default:
		return nil, fmt.Errorf("unsupported key type %T", key)
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}
