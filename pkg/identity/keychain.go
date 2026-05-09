package identity

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// TokenStore persists the OAuth device-code refresh / access tokens.
// §6.3 mandates the OS-native keychain on developer hosts:
// macOS Keychain, Windows Credential Manager, libsecret on Linux. The
// keychain library transparently picks the appropriate backend.
type TokenStore interface {
	Save(label string, token string) error
	Load(label string) (string, error)
	Delete(label string) error
}

// ErrTokenNotFound signals that no token is cached under the label.
var ErrTokenNotFound = errors.New("identity: token not found in keychain")

// KeychainStore implements TokenStore against the OS keychain.
type KeychainStore struct {
	// Service is the namespace under which entries are stored. The
	// spec recommends a stable, unique service name per registry
	// endpoint so multiple deployments do not clash on a single host
	// (§6.3 PODIUM_TOKEN_KEYCHAIN_NAME).
	Service string
}

// Save stores the token under (Service, label).
func (k KeychainStore) Save(label, token string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	return keyring.Set(k.Service, label, token)
}

// Load returns the token previously stored under (Service, label).
// Maps a missing entry to ErrTokenNotFound.
func (k KeychainStore) Load(label string) (string, error) {
	if k.Service == "" {
		return "", errors.New("keychain: Service is required")
	}
	tok, err := keyring.Get(k.Service, label)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s", ErrTokenNotFound, label)
		}
		return "", err
	}
	return tok, nil
}

// Delete removes the token under (Service, label).
func (k KeychainStore) Delete(label string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	if err := keyring.Delete(k.Service, label); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// MemoryStore is an in-memory TokenStore for tests and for
// CI / headless deployments where the OS keychain is unavailable.
type MemoryStore struct {
	entries map[string]string
}

// NewMemoryStore returns a fresh in-memory TokenStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{entries: map[string]string{}} }

// Save stores the token in memory.
func (m *MemoryStore) Save(label, token string) error {
	m.entries[label] = token
	return nil
}

// Load returns the token or ErrTokenNotFound.
func (m *MemoryStore) Load(label string) (string, error) {
	tok, ok := m.entries[label]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrTokenNotFound, label)
	}
	return tok, nil
}

// Delete removes the entry.
func (m *MemoryStore) Delete(label string) error {
	delete(m.entries, label)
	return nil
}
