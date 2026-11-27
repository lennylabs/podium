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
		`[{"signers":[{"issuer":"i","identity":"a@b"}],"optional":true}]`,
		`[{"signers":[{"issuer":"i","identity":"a@b"}],"covers_layer":true}]`,
		`[{"signers":[{"issuer":1}]}]`,
	} {
		var reqs []TrustRequirement
		if err := json.Unmarshal([]byte(body), &reqs); err == nil {
			t.Errorf("%s decoded", body)
		}
	}
	var reqs []TrustRequirement
	if err := json.Unmarshal([]byte(`[{"signers":[{"issuer":"i","identity":"a@b","any_ref":true}],"predicate_type":"p"}]`), &reqs); err != nil ||
		reqs[0].PredicateType != "p" || !reqs[0].Signers[0].AnyRef {
		t.Errorf("valid policy: %+v, %v", reqs, err)
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
