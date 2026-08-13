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

// RefreshLabel derives the keychain label under which the refresh token for
// a registry is stored. The access token keeps the bare registry label so
// existing readers are unaffected; the refresh token is stored alongside it
// under this derived label. spec: §6.3 / §7.7 — cache the access and refresh
// tokens for silent renewal. Shared by the CLI (`podium login`) and the MCP
// bridge so both halves of the device-code credential are addressed the same
// way.
func RefreshLabel(registry string) string {
	return registry + "#refresh"
}

// KeychainStore implements TokenStore against the OS keychain.
type KeychainStore struct {
	// Service is the namespace under which entries are stored. The
	// spec recommends a stable, unique service name per registry
	// endpoint so multiple deployments do not clash on a single host
	// (§6.3 PODIUM_TOKEN_KEYCHAIN_NAME).
	Service string
}

// keychainChunkSize is the per-entry payload ceiling. The go-keyring macOS
// backend rejects values over ~3000 bytes (ErrSetDataTooBig); AD FS access
// and refresh tokens routinely exceed it, so larger tokens are split across
// numbered entries.
const keychainChunkSize = 2500

// keychainChunkMarker prefixes the value stored under the bare label when
// the token is chunked. The suffix is the chunk count. A JWT never starts
// with this prefix, so unchunked readers see it as an invalid cached token
// and fall back to re-authentication.
const keychainChunkMarker = "__podium_chunked__:"

// Save stores the token under (Service, label). A token larger than the
// backend's per-entry ceiling is split across numbered entries
// (label#chunk0..N-1) with a marker under the bare label.
func (k KeychainStore) Save(label, token string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	if len(token) <= keychainChunkSize {
		return keyring.Set(k.Service, label, token)
	}
	var n int
	for i := 0; i < len(token); i += keychainChunkSize {
		end := i + keychainChunkSize
		if end > len(token) {
			end = len(token)
		}
		if err := keyring.Set(k.Service, fmt.Sprintf("%s#chunk%d", label, n), token[i:end]); err != nil {
			return err
		}
		n++
	}
	return keyring.Set(k.Service, label, fmt.Sprintf("%s%d", keychainChunkMarker, n))
}

// Load returns the token previously stored under (Service, label),
// reassembling chunked entries. Maps a missing entry to ErrTokenNotFound.
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
	var n int
	if _, serr := fmt.Sscanf(tok, keychainChunkMarker+"%d", &n); serr != nil || n <= 0 {
		return tok, nil
	}
	var out []byte
	for i := 0; i < n; i++ {
		part, err := keyring.Get(k.Service, fmt.Sprintf("%s#chunk%d", label, i))
		if err != nil {
			return "", fmt.Errorf("keychain: chunk %d/%d under %s: %w", i, n, label, err)
		}
		out = append(out, part...)
	}
	return string(out), nil
}

// Delete removes the token under (Service, label), including any chunked
// entries.
func (k KeychainStore) Delete(label string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	if tok, err := keyring.Get(k.Service, label); err == nil {
		var n int
		if _, serr := fmt.Sscanf(tok, keychainChunkMarker+"%d", &n); serr == nil && n > 0 {
			for i := 0; i < n; i++ {
				_ = keyring.Delete(k.Service, fmt.Sprintf("%s#chunk%d", label, i))
			}
		}
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
