package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §8.6 — first call to loadOrGenerateAuditSigner with a
// missing path generates a fresh keypair and persists it.
// Subsequent calls return the same keypair so the chain head and
// signer key_id stay stable across server restarts.
func TestLoadOrGenerateAuditSigner_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")

	signer1, got1, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got1 != path {
		t.Errorf("first call resolved %q, want %q", got1, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("key file not created: %v", err)
	}

	signer2, got2, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got2 != path {
		t.Errorf("second call resolved %q, want %q", got2, path)
	}
	if !signer1.PublicKey.Equal(signer2.PublicKey) {
		t.Errorf("anchor key changed across reloads: %s vs %s", sign.KeyIDFor(signer1.PublicKey), sign.KeyIDFor(signer2.PublicKey))
	}
}

// Spec: §8.6, §13.12 — an empty path resolves to the default
// ~/.podium/standalone/audit.key under the home directory, and the resolved
// path is returned.
func TestLoadOrGenerateAuditSigner_DefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_, got, err := loadOrGenerateAuditSigner("")
	if err != nil {
		t.Fatalf("loadOrGenerateAuditSigner: %v", err)
	}
	if want := filepath.Join(home, ".podium", "standalone", "audit.key"); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
}

// Spec: §8.6 — the persisted key file uses the registry key-file format
// readOrCreateKeyFile reads and writes; an existing key is loaded
// byte-identical, and a generated file is owner-only.
func TestReadOrCreateKeyFile_LoadsExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")
	kf1, err := readOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	kf2, err := readOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !kf1.Private.Equal(kf2.Private) {
		t.Errorf("private key changed across reloads")
	}
	if !kf1.Public.Equal(kf2.Public) {
		t.Errorf("public key changed across reloads")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("generated key file mode = %o, want 600", mode)
	}
}

// Spec: §8.6, §13.12 — readOrCreateKeyFile generates only for an absent file.
// A malformed file and a public-only file are refused and left unchanged, so a
// damaged key is never silently replaced by a fresh one.
func TestReadOrCreateKeyFile_RefusesWithoutGenerating(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	for name, body := range map[string]string{
		"malformed":   "public: !!!\n",
		"public only": "public: " + base64.StdEncoding.EncodeToString(pub) + "\n",
	} {
		path := filepath.Join(t.TempDir(), "audit.key")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOrCreateKeyFile(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: readOrCreateKeyFile = %v, want an error naming %s", name, err, path)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("%s: key file rewritten to %q", name, got)
		}
	}
}

// Spec: §8.6 — the anchor signer ignores verify: lines, because the §8.6 key
// is not rotated through a verification key set.
func TestLoadOrGenerateAuditSigner_IgnoresVerifyLines(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	extra, _, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "audit.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub, Verify: []ed25519.PublicKey{extra}}); err != nil {
		t.Fatal(err)
	}
	rk, got, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("loadOrGenerateAuditSigner: %v", err)
	}
	if got != path {
		t.Errorf("resolved %q, want %q", got, path)
	}
	if len(rk.Trusted) != 0 || !rk.PublicKey.Equal(pub) {
		t.Errorf("audit signer = %+v; want the file's keypair and no trusted keys", rk)
	}
}

// Spec: §13.12 — an absent key file whose directory refuses the write is an
// error, and the registry loader surfaces a malformed key file rather than
// replacing it.
func TestReadOrCreateKeyFile_GenerateAndLoaderFailures(t *testing.T) {
	if os.Geteuid() != 0 {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := readOrCreateKeyFile(filepath.Join(locked, "k")); err == nil {
			t.Error("readOrCreateKeyFile into a read-only directory succeeded")
		}
	}
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := os.WriteFile(path, []byte("public: !!!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadRegistrySigner(path, true)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("loadRegistrySigner(malformed) = %v, want an error naming %s", err, path)
	}
	if !errors.Is(err, sign.ErrRegistryManagedUnavailable) || !strings.Contains(err.Error(), "config.signature_provider_unavailable") {
		t.Errorf("loadRegistrySigner(malformed) = %v, want config.signature_provider_unavailable wrapping sign.ErrRegistryManagedUnavailable", err)
	}
}

// Spec: §4.7.9 — the registry signer carries the key file's verify: lines as
// its verification-only keys, so a row the retired key signed still verifies.
func TestLoadRegistrySigner_ReadsVerifyLines(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	retiredPub, retiredPriv, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub, Verify: []ed25519.PublicKey{retiredPub}}); err != nil {
		t.Fatal(err)
	}
	provider, err := loadRegistrySigner(path, true)
	if err != nil {
		t.Fatalf("loadRegistrySigner: %v", err)
	}
	hash := "sha256:" + strings.Repeat("ab", 32)
	envelope, err := (sign.RegistryManagedKey{PrivateKey: retiredPriv}).Sign(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("verify of a retired-key envelope: %v", err)
	}
}

// writeAnchorKeyFile writes a fresh keypair to a key file in dir and returns
// the path and the key.
func writeAnchorKeyFile(t *testing.T, dir, name string) (string, sign.RegistryManagedKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub}); err != nil {
		t.Fatalf("WriteKeyFile: %v", err)
	}
	return path, sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}
}

// Spec: §8.6, §13.12 — loadAnchorSigner reads nothing with the interval at 0,
// refuses an unopenable file sink before it reads the key, warns and skips an
// endpoint sink, refuses an unusable key with config.audit_anchor_key_unavailable
// (never classified as a registry-key failure), and refuses a key the registry
// key file carries only while registry signing is on.
func TestLoadAnchorSigner(t *testing.T) {
	dir := t.TempDir()
	logSink, err := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(dir, "malformed.key")
	if err := os.WriteFile(malformed, []byte("public: !!!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	publicOnly := filepath.Join(dir, "public-only.key")
	if err := sign.WriteKeyFile(publicOnly, sign.KeyFile{Public: pub}); err != nil {
		t.Fatal(err)
	}
	sharedPath, sharedKey := writeAnchorKeyFile(t, dir, "shared.key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", sharedPath)
	sinkErr := errors.New("audit: open /x: is a directory")

	cases := []struct {
		name       string
		interval   int
		keyPath    string
		file       *audit.FileSink
		sinkErr    error
		signingOn  bool
		wantOn     bool
		wantCode   string
		wantAbsent bool
	}{
		{name: "sink error refuses before the key", interval: 60, keyPath: filepath.Join(dir, "never-sink.key"), sinkErr: sinkErr, wantCode: "config.audit_sink_unavailable", wantAbsent: true},
		{name: "sink error with interval 0", keyPath: filepath.Join(dir, "never-off.key"), sinkErr: sinkErr, wantAbsent: true},
		{name: "interval 0 with an unreachable key", keyPath: filepath.Join(blocker, "audit.key"), file: logSink},
		{name: "interval 0 with an absent key", keyPath: filepath.Join(dir, "absent-off.key"), file: logSink, wantAbsent: true},
		{name: "endpoint sink", interval: 60, keyPath: filepath.Join(dir, "never-endpoint.key"), wantAbsent: true},
		{name: "absent key is generated", interval: 60, keyPath: filepath.Join(dir, "generated.key"), file: logSink, wantOn: true},
		{name: "malformed key", interval: 60, keyPath: malformed, file: logSink, wantCode: "config.audit_anchor_key_unavailable"},
		{name: "public-only key", interval: 60, keyPath: publicOnly, file: logSink, wantCode: "config.audit_anchor_key_unavailable"},
		{name: "shared key with signing off", interval: 60, keyPath: sharedPath, file: logSink, wantOn: true},
		{name: "shared key with signing on", interval: 60, keyPath: sharedPath, file: logSink, signingOn: true, wantCode: "config.audit_anchor_key_shared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{auditAnchorInterval: tc.interval, auditSigningKeyPath: tc.keyPath}
			key, on, err := loadAnchorSigner(cfg, tc.file, tc.sinkErr, sharedKey, tc.signingOn)
			if on != tc.wantOn {
				t.Errorf("anchoring on = %v, want %v", on, tc.wantOn)
			}
			switch {
			case tc.wantCode == "" && err != nil:
				t.Fatalf("loadAnchorSigner: %v", err)
			case tc.wantCode != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantCode)):
				t.Fatalf("err = %v, want a %s refusal", err, tc.wantCode)
			}
			if errors.Is(err, sign.ErrRegistryManagedUnavailable) {
				t.Errorf("err %v wraps sign.ErrRegistryManagedUnavailable", err)
			}
			if tc.wantCode == "config.audit_sink_unavailable" && !errors.Is(err, tc.sinkErr) {
				t.Errorf("err %v does not wrap the sink error", err)
			}
			if tc.wantCode == "config.audit_anchor_key_unavailable" && !strings.Contains(err.Error(), tc.keyPath) {
				t.Errorf("err %v does not name %s", err, tc.keyPath)
			}
			if !tc.wantOn && key.PrivateKey != nil {
				t.Errorf("a key was returned with anchoring off")
			}
			_, statErr := os.Stat(tc.keyPath)
			if tc.wantAbsent && !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("key path %s exists or is unreadable (%v), want it absent", tc.keyPath, statErr)
			}
			if tc.wantOn && statErr != nil {
				t.Errorf("key path %s: %v, want the key file", tc.keyPath, statErr)
			}
		})
	}
}

// Spec: §8.6, §13.12 — with the anchor key path unset and no resolvable home
// directory, the refusal names PODIUM_AUDIT_SIGNING_KEY_PATH.
func TestLoadAnchorSigner_UnresolvableDefaultPath(t *testing.T) {
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	_, on, err := loadAnchorSigner(&Config{auditAnchorInterval: 60}, sink, nil, sign.RegistryManagedKey{}, false)
	if on || err == nil || !strings.HasPrefix(err.Error(), "config.audit_anchor_key_unavailable") ||
		!strings.Contains(err.Error(), "PODIUM_AUDIT_SIGNING_KEY_PATH") {
		t.Errorf("loadAnchorSigner = (%v, %v), want a config.audit_anchor_key_unavailable refusal naming PODIUM_AUDIT_SIGNING_KEY_PATH", on, err)
	}
}

// Spec: §8.6, §13.12 — an anchor key equal to the registry public key or to any
// verify: key is refused, the refusal names the role, the key_id, and both
// paths, and it carries no key material; a distinct key is admitted.
func TestRefuseSharedAnchorKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, anchor := writeAnchorKeyFile(t, dir, "anchor.key")
	_, registry := writeAnchorKeyFile(t, dir, "registry.key")
	_, other := writeAnchorKeyFile(t, dir, "other.key")
	const anchorPath, registryPath = "/keys/audit.key", "/keys/registry-signing.key"

	cases := []struct {
		name     string
		registry sign.RegistryManagedKey
		wantRole string
	}{
		{name: "registry public key", registry: anchor, wantRole: "as its signing key"},
		{name: "second verify key", registry: sign.RegistryManagedKey{
			PrivateKey: registry.PrivateKey, PublicKey: registry.PublicKey,
			Trusted: []ed25519.PublicKey{other.PublicKey, anchor.PublicKey},
		}, wantRole: "as its verify: key"},
		{name: "distinct key", registry: sign.RegistryManagedKey{
			PrivateKey: registry.PrivateKey, PublicKey: registry.PublicKey,
			Trusted: []ed25519.PublicKey{other.PublicKey},
		}},
	}
	for _, tc := range cases {
		err := refuseSharedAnchorKey(anchor, anchorPath, tc.registry, registryPath)
		if tc.wantRole == "" {
			if err != nil {
				t.Errorf("%s: refuseSharedAnchorKey = %v, want nil", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: refuseSharedAnchorKey = nil, want a refusal", tc.name)
		}
		msg := err.Error()
		for _, want := range []string{"config.audit_anchor_key_shared", tc.wantRole, sign.KeyIDFor(anchor.PublicKey), anchorPath, registryPath} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: %q does not contain %q", tc.name, msg, want)
			}
		}
		for _, secret := range []string{base64.StdEncoding.EncodeToString(anchor.PrivateKey), base64.StdEncoding.EncodeToString(anchor.PublicKey)} {
			if strings.Contains(msg, secret) {
				t.Errorf("%s: refusal carries key material", tc.name)
			}
		}
	}
}
