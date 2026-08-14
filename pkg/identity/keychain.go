package identity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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

// keychainChunkSize is the per-entry payload ceiling. AD FS access and refresh
// tokens routinely exceed it, so larger tokens are split across numbered
// entries.
//
// macOS sets the binding limit. go-keyring base64-encodes the value and passes
// it to `security add-generic-password` inside a command string it rejects over
// 4096 bytes (keyring_darwin.go). Base64 expands by 4/3, so the payload ceiling
// is the largest n satisfying 4*ceil(n/3) + len(service) + len(label) + 55 <=
// 4096. That puts it near 2980 bytes for a short label and lowers it as the
// registry URL in the label grows, which is why the constant is not set closer
// to 3000. Windows caps CredentialBlob at CRED_MAX_CREDENTIAL_BLOB_SIZE
// (5*512 = 2560) and go-keyring writes UTF-8 bytes, so 2500 clears that too.
const keychainChunkSize = 2500

// keychainMaxChunks bounds the stale-chunk sweep. It caps the work when the
// backend reports something other than ErrNotFound for an absent entry, and
// 64 chunks is far beyond any token an identity provider issues.
const keychainMaxChunks = 64

// The backend operations are reached through package-level variables so a test
// can fail one operation partway through a sequence. go-keyring's mock fails
// every operation at once, which cannot reproduce a Save interrupted between
// two chunk writes, and that interruption is what the chunk ordering defends
// against.
var (
	keyringSet    = keyring.Set
	keyringGet    = keyring.Get
	keyringDelete = keyring.Delete
)

// keychainChunkMarker prefixes the value stored under the bare label when
// the token is chunked. The suffix is the chunk count. A JWT never starts
// with this prefix, so unchunked readers see it as an invalid cached token
// and fall back to re-authentication.
const keychainChunkMarker = "__podium_chunked__:"

// chunkLabel names the entry holding chunk i of a chunked token.
func chunkLabel(label string, i int) string {
	return fmt.Sprintf("%s#chunk%d", label, i)
}

// chunkMarkerCount returns the chunk count a marker value declares, or 0 when
// the value is an ordinary token. The count is parsed exactly, so a value that
// merely starts with the marker prefix is treated as an ordinary token.
func chunkMarkerCount(value string) int {
	rest, ok := strings.CutPrefix(value, keychainChunkMarker)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// storedChunkCount reports how many chunks the entry under label currently
// declares. A missing entry or an ordinary token yields 0, as does a backend
// that cannot be read: the write that follows reports the failure, and the
// marker it lands overwrites whatever was there.
func (k KeychainStore) storedChunkCount(label string) int {
	value, err := keyringGet(k.Service, label)
	if err != nil {
		return 0
	}
	return chunkMarkerCount(value)
}

// sweepChunksFrom removes label#chunk<from> upward until the backend reports no
// such entry. A token that shrinks leaves the tail of the previous one behind,
// and a Save interrupted between chunk writes leaves chunks no marker names, so
// both cases are cleaned by sweeping rather than by trusting a stored count.
func (k KeychainStore) sweepChunksFrom(label string, from int) error {
	for i := from; i < from+keychainMaxChunks; i++ {
		err := keyringDelete(k.Service, chunkLabel(label, i))
		if err == nil {
			continue
		}
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// Save stores the token under (Service, label). A token larger than the
// backend's per-entry ceiling is split across numbered entries
// (label#chunk0..N-1) with a marker under the bare label.
//
// A previously chunked entry has its marker removed before the new chunks are
// written. Without that step, a Save interrupted between two chunk writes (a
// signal during `podium login`, a denied keychain prompt, a session bus that
// drops) leaves the old marker pointing at a mix of new and old chunks, and
// Load splices them into a token that authenticates nowhere. With the marker
// gone first, an interrupted Save reads back as no cached token and the caller
// re-authenticates. An entry that is not chunked keeps its value until the new
// one is written, so a failed Save leaves the previous token usable.
func (k KeychainStore) Save(label, token string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	if k.storedChunkCount(label) > 0 {
		if err := keyringDelete(k.Service, label); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
	}
	if len(token) <= keychainChunkSize {
		if err := k.sweepChunksFrom(label, 0); err != nil {
			return err
		}
		return keyringSet(k.Service, label, token)
	}
	var n int
	for i := 0; i < len(token); i += keychainChunkSize {
		end := i + keychainChunkSize
		if end > len(token) {
			end = len(token)
		}
		if err := keyringSet(k.Service, chunkLabel(label, n), token[i:end]); err != nil {
			return err
		}
		n++
	}
	// Drop the previous token's surplus chunks before the marker lands, so an
	// error here leaves no marker and the entry reads as absent.
	if err := k.sweepChunksFrom(label, n); err != nil {
		return err
	}
	return keyringSet(k.Service, label, fmt.Sprintf("%s%d", keychainChunkMarker, n))
}

// Load returns the token previously stored under (Service, label),
// reassembling chunked entries. Maps a missing entry to ErrTokenNotFound.
func (k KeychainStore) Load(label string) (string, error) {
	if k.Service == "" {
		return "", errors.New("keychain: Service is required")
	}
	tok, err := keyringGet(k.Service, label)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s", ErrTokenNotFound, label)
		}
		return "", err
	}
	n := chunkMarkerCount(tok)
	if n == 0 {
		return tok, nil
	}
	var out []byte
	for i := 0; i < n; i++ {
		part, err := keyringGet(k.Service, chunkLabel(label, i))
		if err != nil {
			return "", fmt.Errorf("keychain: chunk %d/%d under %s: %w", i, n, label, err)
		}
		out = append(out, part...)
	}
	return string(out), nil
}

// Delete removes the token under (Service, label), including any chunked
// entries. The sweep runs from the first chunk regardless of what the label
// holds, because the marker naming those chunks is gone once a shorter token
// overwrote it, and an interrupted Save can leave chunks no marker names.
// Logout has to remove that token material either way.
//
// The bare label goes first so a sweep that fails partway still leaves logout
// having removed the entry the rest of the CLI reads. The sweep error is
// reported once the sweep is done, so the caller learns the keychain still
// holds token material.
func (k KeychainStore) Delete(label string) error {
	if k.Service == "" {
		return errors.New("keychain: Service is required")
	}
	err := keyringDelete(k.Service, label)
	if errors.Is(err, keyring.ErrNotFound) {
		err = nil
	}
	if sweepErr := k.sweepChunksFrom(label, 0); sweepErr != nil && err == nil {
		err = sweepErr
	}
	return err
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
