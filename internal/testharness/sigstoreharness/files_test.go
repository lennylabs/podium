package sigstoreharness

import (
	"encoding/json"
	"os"
	"testing"
)

// Spec: §4.7.9, §6.2. WriteFiles writes trusted_root.json and the named
// envelopes: a default envelope, one whose leaf SAN is bob@acme.com, and
// one in the pre-tlog format with a top-level log_index.
func TestWriteFiles(t *testing.T) {
	t.Parallel()
	h := New(t)
	files := h.WriteFiles(t, t.TempDir())
	root, err := os.ReadFile(files.TrustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	decodeRoot(t, root)
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(files.Envelopes[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(raw)
	}
	_, digest := testContentHash("acme sigstore harness content")
	valid := inspect(t, read(EnvelopeValid))
	assertBodyBinds(t, valid, digest)
	if files.ContentHash == "" || valid.leaf.EmailAddresses[0] != DefaultSAN {
		t.Fatalf("content hash %q, SAN %v", files.ContentHash, valid.leaf.EmailAddresses)
	}
	if got := inspect(t, read(EnvelopeForeignSAN)).leaf.EmailAddresses[0]; got != "bob@acme.com" {
		t.Fatalf("foreign SAN = %s", got)
	}
	var legacy map[string]any
	if err := json.Unmarshal([]byte(read(EnvelopeLegacy)), &legacy); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacy["tlog"]; ok || legacy["log_index"] != float64(7) || legacy["cert"] != valid.env.Cert {
		t.Fatalf("legacy envelope = %v", legacy)
	}
}
