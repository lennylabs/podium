package sigstoreharness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Names of the envelopes WriteFiles writes.
const (
	// EnvelopeValid is a default envelope for DefaultSAN and DefaultIssuer.
	EnvelopeValid = "valid"
	// EnvelopeForeignSAN is an envelope whose leaf SAN is bob@acme.com.
	EnvelopeForeignSAN = "foreign-san"
	// EnvelopeLegacy is an envelope in the format that predates the tlog
	// object: cert, signature, and a top-level log_index.
	EnvelopeLegacy = "legacy"
)

// Files names what WriteFiles wrote.
type Files struct {
	// TrustedRoot is the path of trusted_root.json.
	TrustedRoot string
	// ContentHash is the "sha256:hex" content hash every envelope signs.
	ContentHash string
	// Envelopes maps an envelope name to the path of its JSON file.
	Envelopes map[string]string
}

// WriteFiles writes the default trusted root as trusted_root.json and the
// named envelopes as <name>.json under dir, for tests that drive the
// podium binary.
//
// Spec: §4.7.9, §6.2.
func (h *Harness) WriteFiles(t testing.TB, dir string) Files {
	t.Helper()
	sum := sha256.Sum256([]byte("acme sigstore harness content"))
	contentHash := "sha256:" + hex.EncodeToString(sum[:])
	valid := h.Envelope(t, contentHash)
	envelopes := map[string]string{
		EnvelopeValid:      valid,
		EnvelopeForeignSAN: h.Envelope(t, contentHash, WithSAN("bob@acme.com")),
		EnvelopeLegacy:     legacyEnvelope(t, valid),
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

// legacyEnvelope rewrites a current envelope into the pre-tlog format.
func legacyEnvelope(t testing.TB, current string) string {
	t.Helper()
	var env envelopeJSON
	must(t, json.Unmarshal([]byte(current), &env))
	out, err := json.Marshal(struct {
		Cert      string `json:"cert"`
		Signature string `json:"signature"`
		LogIndex  int64  `json:"log_index"`
	}{env.Cert, env.Signature, 7})
	must(t, err)
	return string(out)
}
