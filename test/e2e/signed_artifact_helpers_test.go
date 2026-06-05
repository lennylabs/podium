package e2e

// Signed-artifact ingest and tamper fixture.
//
// The standalone filesystem bootstrap attaches no signatures, so a signed
// artifact whose stored bytes are then tampered was inexpressible end to end.
// The §4.7.9 signing path is unit-tested in pkg/sign (a registry-managed
// Ed25519 keypair produces a detached envelope at ingest), and the §6.6
// content-hash tamper path is driven through the real podium-mcp binary by
// mbStubRegistry (manifest_body_test.go). This file lifts both into one
// reusable primitive: a registry stub that serves a load_artifact response
// carrying a valid registry-managed signature from an offline keypair, plus a
// tamper hook so the consumer-side verifier can be asserted both ways — a valid
// signature loads, a tampered blob is refused.
//
// The signature is produced by the real sign.RegistryManagedKey.Sign over the
// canonical content hash, so the envelope is byte-identical to what the ingest
// pipeline (internal/serverboot/signing.go) attaches. The verifier is the real
// podium-mcp materialize path (enforceSignaturePolicy -> sign.EnforceVerification),
// configured via PODIUM_SIGNATURE_PROVIDER=registry-managed plus
// PODIUM_SIGNATURE_VERIFY_KEY (the offline keypair's base64 public key) and an
// enforcing PODIUM_VERIFY_SIGNATURES. Driving the shipped binary keeps the
// fixture faithful to the consumer verification wiring rather than re-asserting
// the pkg/sign unit behavior.
//
// Spec: §4.7.9 (each version is signed by a registry-managed key at ingest;
// the MCP server verifies on materialization for sensitivity >= medium;
// signature failure aborts with materialize.signature_invalid), §6.2
// (PODIUM_VERIFY_SIGNATURES: never | medium-and-above | always), §6.6 step 2
// (content-hash match over the delivered bytes).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// signedArtifactFixture is a registry stub that serves one signed artifact over
// /v1/load_artifact. It owns an offline Ed25519 keypair, signs the artifact's
// canonical content hash with the real registry-managed signer, and serves the
// resulting envelope alongside the content hash and sensitivity. Tamper mutates
// the served bytes after construction so a subsequent load is refused; consumer
// env (provider, verify key, key id, registry URL) is exposed via Env.
type signedArtifactFixture struct {
	ts       *httptest.Server
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	keyID    string
	signedAt string // the content hash the signature was produced over

	mu          sync.Mutex
	id          string
	typ         string
	version     string
	sensitivity string
	frontmatter string // the served ARTIFACT.md bytes (slot 0 of the content hash)
	contentHash string // the served content_hash field
	signature   string // the served signature envelope

	loadHits int
}

// signedArtifactSpec configures a signedArtifactFixture. ID is the canonical
// artifact id. Frontmatter is the full ARTIFACT.md the stub serves (frontmatter
// plus body for a context artifact); when empty a default medium-sensitivity
// context artifact is synthesized. Sensitivity defaults to "medium" so the
// medium-and-above verification policy engages. KeyID, when set, is embedded in
// the signature envelope and exposed as PODIUM_SIGNATURE_KEY_ID so key-pinning
// can be exercised.
type signedArtifactSpec struct {
	ID          string
	Type        string
	Version     string
	Sensitivity string
	Frontmatter string
	KeyID       string
}

// newSignedArtifactFixture generates an offline Ed25519 keypair, computes the
// artifact's canonical content hash, signs it with the real registry-managed
// signer, and starts an httptest registry that serves the signed load_artifact
// response. The fixture and its server are torn down in t.Cleanup.
func newSignedArtifactFixture(t *testing.T, spec signedArtifactSpec) *signedArtifactFixture {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate offline keypair: %v", err)
	}

	id := spec.ID
	if id == "" {
		id = "finance/secret-policy"
	}
	typ := spec.Type
	if typ == "" {
		typ = "context"
	}
	ver := spec.Version
	if ver == "" {
		ver = "1.0.0"
	}
	sens := spec.Sensitivity
	if sens == "" {
		sens = "medium"
	}
	fm := spec.Frontmatter
	if fm == "" {
		fm = "---\ntype: " + typ + "\nversion: " + ver + "\nsensitivity: " + sens +
			"\ndescription: A signed medium-sensitivity policy artifact.\n---\n\nSigned policy body.\n"
	}

	// Canonical content hash for a non-skill, no-resource artifact: the served
	// ARTIFACT.md bytes in slot 0, an empty skill_raw slot in slot 1. This
	// reproduces the registry's contentHashOf, which the consumer's
	// verifyContentHash recomputes (§6.6 step 2).
	contentHash := "sha256:" + version.ContentHash([]byte(fm), []byte(""))

	signer := sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub, KeyID: spec.KeyID}
	envelope, err := signer.Sign(context.Background(), contentHash)
	if err != nil {
		t.Fatalf("sign content hash: %v", err)
	}

	f := &signedArtifactFixture{
		priv:        priv,
		pub:         pub,
		keyID:       spec.KeyID,
		signedAt:    contentHash,
		id:          id,
		typ:         typ,
		version:     ver,
		sensitivity: sens,
		frontmatter: fm,
		contentHash: contentHash,
		signature:   envelope,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.loadHits++
		resp := map[string]any{
			"id":            f.id,
			"type":          f.typ,
			"version":       f.version,
			"sensitivity":   f.sensitivity,
			"content_hash":  f.contentHash,
			"frontmatter":   f.frontmatter,
			"manifest_body": f.frontmatter,
			"signature":     f.signature,
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	return f
}

// PublicKeyB64 returns the offline keypair's base64-encoded public key, the
// value PODIUM_SIGNATURE_VERIFY_KEY carries so the consumer-side
// registry-managed provider can verify the envelope.
func (f *signedArtifactFixture) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(f.pub)
}

// Env returns the env var set that points the real podium-mcp binary at this
// fixture's registry with the registry-managed verifier configured: the
// provider, the offline public key, the optional key id, and an enforcing
// verification policy. HOME and the cache dir are pinned to caller-supplied
// fresh temp dirs so the bridge never reads the developer's environment.
func (f *signedArtifactFixture) Env(t *testing.T, policy string) []string {
	t.Helper()
	env := []string{
		"PODIUM_REGISTRY=" + f.ts.URL,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"HOME=" + t.TempDir(),
		"PODIUM_HARNESS=none",
		"PODIUM_VERIFY_SIGNATURES=" + policy,
		"PODIUM_SIGNATURE_PROVIDER=registry-managed",
		"PODIUM_SIGNATURE_VERIFY_KEY=" + f.PublicKeyB64(),
	}
	if f.keyID != "" {
		env = append(env, "PODIUM_SIGNATURE_KEY_ID="+f.keyID)
	}
	return env
}

// ID returns the served artifact id.
func (f *signedArtifactFixture) ID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.id
}

// LoadHits returns how many times the load_artifact route was served. A blocked
// load still reaches the registry (the verifier runs consumer-side after the
// fetch), so this confirms the fixture was actually consulted.
func (f *signedArtifactFixture) LoadHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadHits
}

// TamperContentHash rewrites the served content_hash to a value the offline
// signature does not cover, leaving the signed bytes and the envelope intact.
// The consumer verifies the envelope against the served content_hash first
// (enforceSignaturePolicy runs before verifyContentHash), so this is the
// signed-then-tampered case the operator guide names: the signature no longer
// validates against the (tampered) hash and the load aborts with
// materialize.signature_invalid. The replacement is a syntactically valid
// sha256 hash that differs from the original in its last hex nibble.
func (f *signedArtifactFixture) TamperContentHash() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contentHash = flipLastHexNibble(f.contentHash)
}

// TamperBody mutates the served ARTIFACT.md bytes while leaving the served
// content_hash and signature untouched. The signature still validates against
// the unchanged content_hash, so the signature gate passes, but the §6.6 step 2
// recompute over the tampered bytes no longer matches the served hash and the
// load aborts with materialize.content_hash_mismatch. This is the
// integrity-gate complement to TamperContentHash.
func (f *signedArtifactFixture) TamperBody() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frontmatter += "\n<!-- injected tamper line -->\n"
}

// flipLastHexNibble returns s with its final hexadecimal character changed to a
// different valid hex digit, yielding a well-formed but distinct "sha256:<hex>"
// string. Used to forge a content hash the offline signature does not cover
// without producing a malformed (and separately-rejected) value.
func flipLastHexNibble(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	last := b[len(b)-1]
	if last == '0' {
		b[len(b)-1] = '1'
	} else {
		b[len(b)-1] = '0'
	}
	return string(b)
}
