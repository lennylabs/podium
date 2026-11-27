package sigstoreharness

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Names of the signatures WriteFiles writes.
const (
	// EnvelopeValid is a default keyless bundle for DefaultSAN and
	// DefaultIssuer.
	EnvelopeValid = "valid"
	// EnvelopeForeignSAN is a keyless bundle whose leaf SAN is bob@acme.com.
	EnvelopeForeignSAN = "foreign-san"
	// EnvelopeLegacy is the default signature in the keyless envelope
	// format of 0.7.0, which 0.8.0 refuses: the members cert, signature,
	// tlog, and timestamp.
	EnvelopeLegacy = "legacy"
)

// Files names what WriteFiles wrote.
type Files struct {
	// TrustedRoot is the path of trusted_root.json.
	TrustedRoot string
	// ContentHash is the "sha256:hex" content hash every signature signs.
	ContentHash string
	// Envelopes maps a signature name to the path of its JSON file.
	Envelopes map[string]string
}

// WriteFiles writes the default trusted root as trusted_root.json and the
// named signatures as <name>.json under dir, for tests that drive the
// podium binary.
//
// Spec: §4.7.9, §6.2.
func (h *Harness) WriteFiles(t testing.TB, dir string) Files {
	t.Helper()
	sum := sha256.Sum256([]byte("acme sigstore harness content"))
	contentHash := "sha256:" + hex.EncodeToString(sum[:])
	valid, parts, err := h.buildKeyless(contentHash, defaultEnvConfig())
	must(t, err)
	envelopes := map[string]string{
		EnvelopeValid:      string(valid),
		EnvelopeForeignSAN: h.KeylessBundle(t, contentHash, WithSAN("bob@acme.com")),
		EnvelopeLegacy:     legacyEnvelope(t, parts),
	}
	files := Files{
		TrustedRoot: filepath.Join(dir, "trusted_root.json"),
		ContentHash: contentHash,
		Envelopes:   map[string]string{},
	}
	entries := []testharness.WriteTreeOption{{Path: "trusted_root.json", Content: string(h.TrustedRootJSON())}}
	for name, body := range envelopes {
		entries = append(entries, testharness.WriteTreeOption{Path: name + ".json", Content: body})
		files.Envelopes[name] = filepath.Join(dir, name+".json")
	}
	testharness.WriteTree(t, dir, entries...)
	return files
}

// legacyEnvelope writes the parts of a keyless bundle in the 0.7.0
// envelope format: the PEM chain, the base64 signature, the tlog object
// with the log index, entry body, audit path, and checkpoint, and the
// base64 timestamp token.
func legacyEnvelope(t testing.TB, p keylessParts) string {
	t.Helper()
	var resp struct {
		Status asn1.RawValue
		Token  asn1.RawValue
	}
	_, err := asn1.Unmarshal(p.tsr, &resp)
	must(t, err)
	hashes := make([]string, len(p.entry.InclusionProof.Hashes))
	for i, h := range p.entry.InclusionProof.Hashes {
		hashes[i] = base64.StdEncoding.EncodeToString(h)
	}
	out, err := json.Marshal(map[string]any{
		"cert":      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.leaf.cert.Raw})),
		"signature": base64.StdEncoding.EncodeToString(p.leaf.sig),
		"tlog": map[string]any{
			"log_index":  p.entry.LogIndex,
			"body":       base64.StdEncoding.EncodeToString(p.entry.CanonicalizedBody),
			"hashes":     hashes,
			"checkpoint": p.entry.InclusionProof.Checkpoint.Envelope,
		},
		"timestamp": base64.StdEncoding.EncodeToString(resp.Token.FullBytes),
	})
	must(t, err)
	return string(out)
}
