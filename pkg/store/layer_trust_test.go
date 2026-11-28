package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Spec: §4.7.11 (Layer trust policy) — a requirement or a signer object that
// holds a member of another name is refused, so a misspelled member is an
// error rather than a dropped binding.
func TestTrustPolicy_DecodeRefusesUnknownMembers(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`[{"signers":[{"issuer":"i","identity":"a@b","instance_name":"github"}]}]`,
		`[{"signers":[{"issuer":"i","identity":"a@b"}],"optinal":true}]`,
		`[{"signers":[{"issuer":"i","identity":"a@b"}],"covers_layer":"true"}]`,
		`[{"signers":[{"issuer":1}]}]`,
	} {
		var reqs []TrustRequirement
		if err := json.Unmarshal([]byte(body), &reqs); err == nil {
			t.Errorf("%s decoded", body)
		}
	}
	var reqs []TrustRequirement
	if err := json.Unmarshal([]byte(`[{"signers":[{"issuer":"i","identity":"a@b","any_ref":true}],"predicate_type":"p","covers_layer":true},`+
		`{"signers":[{"issuer":"i","identity":"c@d"}],"optional":true}]`), &reqs); err != nil ||
		reqs[0].PredicateType != "p" || !reqs[0].Signers[0].AnyRef || !reqs[0].CoversLayer || reqs[0].Optional || !reqs[1].Optional {
		t.Errorf("valid policy: %+v, %v", reqs, err)
	}
	// The clone keeps the covering and optional markers.
	if back := cloneTrustRequirements(reqs); !back[0].CoversLayer || !back[1].Optional {
		t.Errorf("clone = %+v", back)
	}
}

// Spec: §4.7.11 (Layer trust policy) — the trust_require_types column holds
// the JSON array, the empty string reads as no type, and a column that does
// not decode fails the read.
func TestRequireTypesColumn(t *testing.T) {
	t.Parallel()
	if col := requireTypesColumn([]string{}); col != "" {
		t.Fatalf("empty column = %q", col)
	}
	col := requireTypesColumn([]string{"hook", "mcp-server"})
	if col != `["hook","mcp-server"]` {
		t.Fatalf("column = %q", col)
	}
	if back, err := parseRequireTypes(col); err != nil || len(back) != 2 || back[1] != "mcp-server" {
		t.Fatalf("round trip = %q, %v", back, err)
	}
	if got, err := parseRequireTypes(""); got != nil || err != nil {
		t.Errorf("empty column read %q, %v", got, err)
	}
	if _, err := parseRequireTypes(`{"hook":1}`); err == nil {
		t.Errorf("a column that does not decode read back")
	}
}

// Spec: §4.7.11 (Layer trust policy) — the SQL column holds the JSON array,
// the empty string reads as no requirement, and a column that does not decode
// fails the read.
func TestTrustRequirementsColumn(t *testing.T) {
	t.Parallel()
	col, err := trustRequirementsColumn(nil)
	if err != nil || col != "" {
		t.Fatalf("empty column = %q, %v", col, err)
	}
	reqs := []TrustRequirement{{Signers: []TrustSigner{{Issuer: "i", Identity: "a@b"}}}}
	col, err = trustRequirementsColumn(reqs)
	if err != nil || !strings.HasPrefix(col, `[{"signers":[`) {
		t.Fatalf("column = %q, %v", col, err)
	}
	back, err := parseTrustRequirements(col)
	if err != nil || len(back) != 1 || back[0].Signers[0] != reqs[0].Signers[0] {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if got, err := parseTrustRequirements(""); got != nil || err != nil {
		t.Errorf("empty column read %+v, %v", got, err)
	}
	if _, err := parseTrustRequirements(`[{"signers":[{"bad":1}]}]`); err == nil {
		t.Errorf("a column that does not decode read back")
	}
}

// Spec: §4.7.11 (Key-based signers) — a signer's public_key reads and
// writes through the trust_requirements column as given, its line breaks
// written as \n.
func TestTrustRequirementsColumn_PublicKey(t *testing.T) {
	t.Parallel()
	pem := "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"
	col, err := trustRequirementsColumn([]TrustRequirement{{Signers: []TrustSigner{{PublicKey: pem}}}})
	if err != nil || !strings.Contains(col, `"public_key":"-----BEGIN PUBLIC KEY-----\nAAAA`) {
		t.Fatalf("column %s, %v", col, err)
	}
	back, err := parseTrustRequirements(col)
	if err != nil || back[0].Signers[0].PublicKey != pem {
		t.Fatalf("read back %+v, %v", back, err)
	}
}
