package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §13.12 / §4.7.9 — a registry with signing on and no
// PODIUM_SIGN_KEY_PATH refuses to start unless its store is the SQLite store
// in the directory the default key resolves to. A memory store strands no
// signature, and signing off or a set key path needs no refusal. The error
// names the backend, PODIUM_SIGN_KEY_PATH, and PODIUM_SIGN=none, and carries
// no DSN.
func TestRefuseUnpersistedSigningKey(t *testing.T) {
	home := t.TempDir()
	defaultDB := filepath.Join(home, ".podium", "standalone", "podium.db")
	otherDB := filepath.Join(home, "elsewhere", "podium.db")
	const dsn = "postgres://podium:hunter2@db.acme.com/podium"
	cases := []struct {
		name    string
		cfg     Config
		keyPath string
		refuse  bool
		want    []string
	}{
		{name: "signing off", cfg: Config{signMode: "none", storeType: "postgres", postgresDSN: dsn}},
		{name: "key path set", cfg: Config{storeType: "postgres", postgresDSN: dsn}, keyPath: "/srv/podium/registry-signing.key"},
		{name: "memory store", cfg: Config{storeType: "memory"}},
		{name: "sqlite in the default directory", cfg: Config{storeType: "sqlite", sqlitePath: defaultDB}},
		{name: "sqlite in the default directory, unclean path", cfg: Config{signMode: "registry-key", storeType: "sqlite", sqlitePath: home + "/.podium/standalone/./podium.db"}},
		{
			name:   "sqlite in another directory",
			cfg:    Config{storeType: "sqlite", sqlitePath: otherDB},
			refuse: true,
			want:   []string{"sqlite", otherDB, "PODIUM_SIGN_KEY_PATH", "PODIUM_SIGN=none"},
		},
		{
			name:   "postgres store",
			cfg:    Config{signMode: "registry-key", storeType: "postgres", postgresDSN: dsn},
			refuse: true,
			want:   []string{"postgres", "PODIUM_SIGN_KEY_PATH", "PODIUM_SIGN=none"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("PODIUM_SIGN_KEY_PATH", tc.keyPath)
			err := refuseUnpersistedSigningKey(&tc.cfg)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("refuseUnpersistedSigningKey = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("refuseUnpersistedSigningKey = nil, want a refusal")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q does not name %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "db.acme.com") {
				t.Errorf("refusal %q carries the DSN", err)
			}
		})
	}
}

// Spec: §13.12 — a home os.UserHomeDir cannot resolve leaves the default key
// path unresolvable, so the start is refused rather than admitted.
func TestRefuseUnpersistedSigningKey_UnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	cfg := Config{storeType: "sqlite", sqlitePath: "/var/lib/podium/podium.db"}
	if err := refuseUnpersistedSigningKey(&cfg); err == nil {
		t.Fatal("refuseUnpersistedSigningKey = nil with no resolvable home, want an error")
	}
}

// Spec: §4.7.9 — the key file the registry writes is the one a consumer
// resolves and reads: a keypair generated through loadRegistrySigner
// under a fixture home, with PODIUM_SIGN_KEY_PATH unset, sits at
// sign.KeyFilePath(""), and both halves read back through sign.ReadKeyFile
// match the provider's own.
func TestRegistrySigningKeyFile_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	signer, err := loadRegistrySigner("", true)
	if err != nil {
		t.Fatalf("loadRegistrySigner: %v", err)
	}
	path, err := sign.KeyFilePath("")
	if err != nil {
		t.Fatalf("KeyFilePath: %v", err)
	}
	if delegated, _ := registrySigningKeyPath(""); delegated != path {
		t.Fatalf("registrySigningKeyPath(\"\") = %q, sign.KeyFilePath(\"\") = %q; want one path", delegated, path)
	}
	kf, err := sign.ReadKeyFile(path)
	if err != nil {
		t.Fatalf("ReadKeyFile: %v", err)
	}
	if !kf.Public.Equal(signer.PublicKey) || !kf.Private.Equal(signer.PrivateKey) {
		t.Errorf("ReadKeyFile = %+v; want the signer's keypair", kf)
	}
}

// writeKeyText writes text to a key file under a fresh directory and returns
// its path.
func writeKeyText(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Spec: §13.12, §4.7.9 — a key file the registry cannot sign and verify under
// refuses the loader with config.signature_provider_unavailable written into
// the text, sign.ErrRegistryManagedUnavailable in the chain, and the path
// named, for both the boot's generating load and the command's reading load.
func TestLoadRegistrySigner_RefusesUnusableKeyFile(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	b64 := base64.StdEncoding.EncodeToString
	cases := map[string]string{
		"public line does not match private": "private: " + b64(priv) + "\npublic: " + b64(otherPub) + "\n",
		"undecodable verify line":            "private: " + b64(priv) + "\npublic: " + b64(pub) + "\nverify: !!!\n",
		"no private line":                    "public: " + b64(pub) + "\n",
	}
	for name, text := range cases {
		for _, generate := range []bool{true, false} {
			t.Run(name+"/generate="+strconv.FormatBool(generate), func(t *testing.T) {
				path := writeKeyText(t, text)
				_, err := loadRegistrySigner(path, generate)
				if err == nil {
					t.Fatalf("loadRegistrySigner(generate=%v) = nil error, want a refusal", generate)
				}
				if !errors.Is(err, sign.ErrRegistryManagedUnavailable) {
					t.Errorf("error %v does not wrap sign.ErrRegistryManagedUnavailable", err)
				}
				if !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable:") || !strings.Contains(err.Error(), path) {
					t.Errorf("error %q, want the config.signature_provider_unavailable prefix and the path %s", err, path)
				}
				if got, _ := os.ReadFile(path); string(got) != text {
					t.Errorf("the refused key file was rewritten")
				}
			})
		}
	}
}

// Spec: §4.7.9 — the verify: lines load into Trusted, the public key is
// derived from the private key, and an envelope a verify: key signed
// verifies under the loaded key.
func TestLoadRegistrySigner_LoadsVerificationKeySet(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	retiredPub, retiredPriv, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub, Verify: []ed25519.PublicKey{retiredPub}}); err != nil {
		t.Fatal(err)
	}
	key, err := loadRegistrySigner(path, false)
	if err != nil {
		t.Fatalf("loadRegistrySigner: %v", err)
	}
	if !key.PublicKey.Equal(pub) || len(key.Trusted) != 1 || !key.Trusted[0].Equal(retiredPub) {
		t.Fatalf("loaded key = public %x, trusted %x; want public %x and trusted [%x]", key.PublicKey, key.Trusted, pub, retiredPub)
	}
	hash := "sha256:" + strings.Repeat("cd", 32)
	envelope, err := (sign.RegistryManagedKey{PrivateKey: retiredPriv}).Sign(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("verify of a verify:-key envelope: %v", err)
	}
}

// Spec: §13.12 — the reading load refuses an absent key file and writes
// nothing; the generating load creates the file at mode 0600.
func TestLoadRegistrySigner_GenerateFlag(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "registry-signing.key")
	_, err := loadRegistrySigner(absent, false)
	if !errors.Is(err, sign.ErrRegistryManagedUnavailable) || !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable:") {
		t.Fatalf("loadRegistrySigner(absent, false) = %v, want a config.signature_provider_unavailable refusal", err)
	}
	if _, statErr := os.Stat(absent); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("loadRegistrySigner(absent, false) left a file behind: %v", statErr)
	}
	key, err := loadRegistrySigner(absent, true)
	if err != nil {
		t.Fatalf("loadRegistrySigner(absent, true): %v", err)
	}
	if key.PrivateKey == nil {
		t.Error("the generated key carries no private key")
	}
	info, err := os.Stat(absent)
	if err != nil {
		t.Fatalf("stat generated key: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("generated key mode = %o, want 600", mode)
	}
}

// Spec: §13.12 — an unresolvable default path refuses the loader with the
// same code, so a start never falls back to an unnamed key.
func TestLoadRegistrySigner_UnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := loadRegistrySigner("", true)
	if !errors.Is(err, sign.ErrRegistryManagedUnavailable) || !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable:") {
		t.Fatalf("loadRegistrySigner with no home = %v, want a config.signature_provider_unavailable refusal", err)
	}
}

// Spec: §13.12, §13.4 — the key is co-located with the store only for a
// SQLite store in the key file's directory.
func TestKeyCoLocatedWithStore(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "registry-signing.key")
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "sqlite in the key's directory", cfg: Config{storeType: "sqlite", sqlitePath: filepath.Join(dir, "podium.db")}, want: true},
		{name: "sqlite in the key's directory, unclean path", cfg: Config{storeType: "sqlite", sqlitePath: dir + "/./podium.db"}, want: true},
		{name: "sqlite in another directory", cfg: Config{storeType: "sqlite", sqlitePath: filepath.Join(dir, "other", "podium.db")}},
		{name: "postgres", cfg: Config{storeType: "postgres", sqlitePath: filepath.Join(dir, "podium.db")}},
		{name: "memory", cfg: Config{storeType: "memory"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keyCoLocatedWithStore(&tc.cfg, keyPath); got != tc.want {
				t.Errorf("keyCoLocatedWithStore = %v, want %v", got, tc.want)
			}
		})
	}
}
