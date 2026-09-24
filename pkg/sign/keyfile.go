package sign

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// PublicKeyFromKeyFile reads the registry-managed Ed25519 verification key
// from a key file written by the registry (internal/serverboot's
// readOrCreateEd25519): a text file whose "public:" line carries the base64
// key. The private half, when the file carries one, is ignored.
//
// Spec: §4.7.9.
func PublicKeyFromKeyFile(path string) (ed25519.PublicKey, error) {
	value, err := keyFileLine(path, "public:")
	if err != nil {
		return nil, err
	}
	pub, err := PublicKeyFromBase64(value)
	if err != nil {
		return nil, fmt.Errorf("sign: key file %s: %w", path, err)
	}
	return pub, nil
}

// PrivateKeyFromKeyFile reads the registry-managed Ed25519 signing key from
// the file PublicKeyFromKeyFile reads, taking its "private:" line. The public
// half is ignored.
//
// Spec: §4.7.9.
func PrivateKeyFromKeyFile(path string) (ed25519.PrivateKey, error) {
	value, err := keyFileLine(path, "private:")
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("sign: key file %s: decode private key: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("sign: key file %s: private key is %d bytes, want %d", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// keyFileLine returns the trimmed value of the first line in the file at path
// whose trimmed text begins with prefix.
func keyFileLine(path, prefix string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sign: read key file: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
		}
	}
	return "", fmt.Errorf("sign: key file %s carries no %q line", path, prefix)
}
