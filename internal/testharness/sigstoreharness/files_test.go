package sigstoreharness

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"testing"
)

// Spec: §4.7.9, §6.2. WriteFiles writes trusted_root.json and the named
// signatures: a default keyless bundle, one whose leaf SAN is
// bob@acme.com, and the default signature in the 0.7.0 envelope format,
// whose members carry the bundle's leaf, signature, log entry, and token.
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
	var legacy struct {
		Cert      string `json:"cert"`
		Signature string `json:"signature"`
		TLog      struct {
			LogIndex   int64  `json:"log_index"`
			Body       string `json:"body"`
			Checkpoint string `json:"checkpoint"`
		} `json:"tlog"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(read(EnvelopeLegacy)), &legacy); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(legacy.Cert))
	if block == nil {
		t.Fatal("legacy cert carries no PEM block")
	}
	equalBytes(t, "legacy cert", block.Bytes, valid.leaf.Raw)
	equalBytes(t, "legacy signature", mustB64(t, legacy.Signature), valid.sig)
	equalBytes(t, "legacy body", mustB64(t, legacy.TLog.Body), valid.bodyRaw)
	if legacy.TLog.LogIndex != valid.entry.LogIndex || legacy.TLog.Checkpoint != valid.checkpoint {
		t.Fatalf("legacy tlog = %+v", legacy.TLog)
	}
	parseToken(t, mustB64(t, legacy.Timestamp))
	if base64.StdEncoding.EncodeToString(valid.sig) != legacy.Signature {
		t.Fatal("legacy signature differs from the bundle's")
	}
}
