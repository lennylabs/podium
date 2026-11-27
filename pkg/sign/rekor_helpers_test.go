package sign

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestSplitContentHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantAlg string
		wantHex string
		wantErr bool
	}{
		{"sha256:" + hex.EncodeToString([]byte("abc")), "sha256", "616263", false},
		{"sha512:" + hex.EncodeToString([]byte("def")), "sha512", "646566", false},
		{"no-colon", "", "", true},
		{":alone", "", "", true},
		{"sha256:", "", "", true},
		{"sha256:not-hex", "", "", true},
	}
	for _, c := range cases {
		alg, hexOut, err := splitContentHash(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("splitContentHash(%q): err = %v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if alg != c.wantAlg || hexOut != c.wantHex {
			t.Errorf("splitContentHash(%q) = (%q, %q), want (%q, %q)",
				c.in, alg, hexOut, c.wantAlg, c.wantHex)
		}
	}
}

// Spec: §4.7.9 — the Rekor v2 response is a protojson TransparencyLogEntry
// that carries the members a bundle's log entry needs: a canonicalized
// body, a log ID, a kind and version, and an inclusion proof with a
// checkpoint. logIndex is a decimal string, and a response that is no
// such entry is refused.
func TestParseRekorEntry(t *testing.T) {
	t.Parallel()
	const proof = `"inclusionProof":{"logIndex":"5","rootHash":"AA==","treeSize":"7","hashes":[],"checkpoint":{"envelope":"acme"}}`
	const body = `"canonicalizedBody":"e30="`
	const logID = `"logId":{"keyId":"AA=="}`
	const kind = `"kindVersion":{"kind":"hashedrekord","version":"0.0.2"}`
	cases := []struct {
		name, raw, want string
	}{
		{"complete", `{"logIndex":"5",` + logID + `,` + kind + `,` + body + `,` + proof + `,"unknownMember":1}`, ""},
		{"not JSON", `acme`, "decode response"},
		{"log index not a number", `{"logIndex":"five",` + logID + `,` + kind + `,` + body + `,` + proof + `}`, "decode response"},
		{"no log ID", `{` + kind + `,` + body + `,` + proof + `}`, "carries no log ID"},
		{"no kind and version", `{` + logID + `,` + body + `,` + proof + `}`, "carries no entry kind and version"},
	}
	for _, c := range cases {
		entry, err := parseRekorEntry([]byte(c.raw))
		if c.want == "" {
			if err != nil || entry.GetLogIndex() != 5 {
				t.Errorf("%s: (%v, %v)", c.name, entry, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
