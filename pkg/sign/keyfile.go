package sign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Key-file line prefixes. A private: line carries the 64-byte signing key, a
// public: line its 32-byte public half, and each verify: line a public key
// trusted for verification only.
const (
	keyLinePrivate = "private:"
	keyLinePublic  = "public:"
	keyLineVerify  = "verify:"
)

// KeyFile is the parsed registry-managed key file. Private is nil for a
// consumer's public-only copy. Public is the signing key's public half, and
// Public with Verify forms the §4.7.9 verification key set.
//
// Spec: §4.7.9, §13.12.
type KeyFile struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
	Verify  []ed25519.PublicKey
}

// KeyFilePath resolves the registry-managed signing key file's location. It
// returns env (the value of PODIUM_SIGN_KEY_PATH) when non-empty and otherwise
// <home>/.podium/standalone/registry-signing.key. The registry that writes the
// file and every consumer that reads it resolve the path through this one
// function, so on a standalone machine the producer and the consumer cannot
// resolve different files.
//
// Spec: §4.7.9, §13.12.
func KeyFilePath(env string) (string, error) {
	if env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sign: resolve key file path: %w", err)
	}
	return filepath.Join(home, ".podium", "standalone", "registry-signing.key"), nil
}

// ParseKeyFile parses the key-file format. A line that does not decode, or
// that decodes to a key of the wrong size, is an error naming its kind. A
// public: line is required, so the verification key set it heads is never
// empty. A private: line is optional so a consumer's public-only copy parses,
// and one whose public half differs from the public: line is an error, because
// the registry would then sign under a key its own set does not name. Lines
// with any other prefix are ignored.
//
// Spec: §4.7.9, §13.12.
func ParseKeyFile(data []byte) (KeyFile, error) {
	var kf KeyFile
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if err := kf.parseLine(line); err != nil {
			return KeyFile{}, err
		}
	}
	if len(kf.Public) == 0 {
		return KeyFile{}, fmt.Errorf("key file carries no %q line", keyLinePublic)
	}
	if kf.Private != nil && !kf.Public.Equal(kf.Private.Public()) {
		return KeyFile{}, fmt.Errorf("the %q line is not the public half of the %q line", keyLinePublic, keyLinePrivate)
	}
	return kf, nil
}

// parseLine decodes one trimmed line into kf. A repeated private: or public:
// line is an error, so a file cannot name two signing keys.
func (kf *KeyFile) parseLine(line string) error {
	switch {
	case strings.HasPrefix(line, keyLinePrivate):
		if kf.Private != nil {
			return fmt.Errorf("key file carries more than one %q line", keyLinePrivate)
		}
		priv, err := decodePrivateKey(strings.TrimPrefix(line, keyLinePrivate))
		if err != nil {
			return fmt.Errorf("%s line: %w", keyLinePrivate, err)
		}
		kf.Private = priv
	case strings.HasPrefix(line, keyLinePublic):
		if kf.Public != nil {
			return fmt.Errorf("key file carries more than one %q line", keyLinePublic)
		}
		pub, err := PublicKeyFromBase64(strings.TrimPrefix(line, keyLinePublic))
		if err != nil {
			return fmt.Errorf("%s line: %w", keyLinePublic, err)
		}
		kf.Public = pub
	case strings.HasPrefix(line, keyLineVerify):
		pub, err := PublicKeyFromBase64(strings.TrimPrefix(line, keyLineVerify))
		if err != nil {
			return fmt.Errorf("%s line: %w", keyLineVerify, err)
		}
		kf.Verify = append(kf.Verify, pub)
	}
	return nil
}

// decodePrivateKey decodes a standard-base64 Ed25519 private key.
func decodePrivateKey(s string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// ReadKeyFile reads and parses the key file at path. Every error names the
// path. An absent file wraps fs.ErrNotExist so a caller that generates the
// file can tell absence from a malformed file.
//
// Spec: §4.7.9, §13.12.
func ReadKeyFile(path string) (KeyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KeyFile{}, fmt.Errorf("sign: read key file %s: %w", path, err)
	}
	kf, err := ParseKeyFile(data)
	if err != nil {
		return KeyFile{}, fmt.Errorf("sign: key file %s: %w", path, err)
	}
	return kf, nil
}

// WriteKeyFile writes kf to path in the key-file format. It creates the parent
// directory with mode 0700, writes a temporary file in that directory with
// mode 0600, and renames it over path, so a reader sees either the previous
// file or the new one and never a partial write.
//
// Spec: §4.7.9, §13.12.
func WriteKeyFile(path string, kf KeyFile) error {
	if len(kf.Public) == 0 {
		return errors.New("sign: write key file: no public key")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sign: write key file %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("sign: write key file %s: %w", path, err)
	}
	// CreateTemp opens the file with mode 0600, so the key never exists
	// under a broader mode. The deferred remove is a no-op after the rename.
	defer func() { _ = os.Remove(tmp.Name()) }()
	// The write and close errors below need a device that fails after the
	// create succeeds (a full disk or an I/O error), which a unit test
	// cannot produce portably.
	if _, err := tmp.Write(encodeKeyFile(kf)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sign: write key file %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("sign: write key file %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("sign: write key file %s: %w", path, err)
	}
	return nil
}

// encodeKeyFile renders kf with the private: line first when present, then the
// public: line, then one verify: line per key in order.
func encodeKeyFile(kf KeyFile) []byte {
	var b bytes.Buffer
	line := func(prefix string, key []byte) {
		b.WriteString(prefix + " " + base64.StdEncoding.EncodeToString(key) + "\n")
	}
	if kf.Private != nil {
		line(keyLinePrivate, kf.Private)
	}
	line(keyLinePublic, kf.Public)
	for _, v := range kf.Verify {
		line(keyLineVerify, v)
	}
	return b.Bytes()
}

// VerificationKeysFromKeyFile returns the verification key set the key file at
// path carries: the public: key followed by every verify: key. It requires no
// private: line. Because ParseKeyFile requires a public: line, it never
// returns an empty set with a nil error.
//
// Spec: §4.7.9.
func VerificationKeysFromKeyFile(path string) ([]ed25519.PublicKey, error) {
	kf, err := ReadKeyFile(path)
	if err != nil {
		return nil, err
	}
	return append([]ed25519.PublicKey{kf.Public}, kf.Verify...), nil
}

// PublicKeysFromList decodes a comma-separated list of standard-base64 Ed25519
// public keys, the value of PODIUM_SIGNATURE_VERIFY_KEY. Space around an entry
// is trimmed. An empty list, an empty entry (a trailing comma included), and an
// entry that does not decode are errors, so a malformed value never yields a
// partial set.
//
// Spec: §4.7.9, §6.2.
func PublicKeysFromList(s string) ([]ed25519.PublicKey, error) {
	entries := strings.Split(s, ",")
	keys := make([]ed25519.PublicKey, 0, len(entries))
	for i, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			return nil, fmt.Errorf("entry %d is empty", i+1)
		}
		pub, err := PublicKeyFromBase64(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i+1, err)
		}
		keys = append(keys, pub)
	}
	return keys, nil
}

// VerificationKeys resolves a consumer's §4.7.9 verification key set.
// verifyEnv is the value of PODIUM_SIGNATURE_VERIFY_KEY and keyPathEnv the
// value of PODIUM_SIGN_KEY_PATH. A non-empty verifyEnv is authoritative, so a
// malformed list is an error naming the variable and never falls through to
// the key file. Otherwise the set is the public: line and every verify: line
// of the key file at KeyFilePath(keyPathEnv). podium-mcp and podium verify
// both resolve through it, so the two consumers cannot resolve different sets.
//
// Spec: §4.7.9, §6.2.
func VerificationKeys(verifyEnv, keyPathEnv string) ([]ed25519.PublicKey, error) {
	if verifyEnv != "" {
		keys, err := PublicKeysFromList(verifyEnv)
		if err != nil {
			return nil, fmt.Errorf("PODIUM_SIGNATURE_VERIFY_KEY: %w", err)
		}
		return keys, nil
	}
	path, err := KeyFilePath(keyPathEnv)
	if err == nil {
		var keys []ed25519.PublicKey
		if keys, err = VerificationKeysFromKeyFile(path); err == nil {
			return keys, nil
		}
	}
	return nil, fmt.Errorf("PODIUM_SIGNATURE_VERIFY_KEY is unset and the registry key file (PODIUM_SIGN_KEY_PATH) yields no usable public key: %w", err)
}
