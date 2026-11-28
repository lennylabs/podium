package main

import (
	"bytes"
	"testing"
)

// Spec: §7.6.1, §7.2 (Attestation members) — the default output prints the
// outcome with its failure and one line per link, quoting each string and
// escaping a control character a certificate can carry; a response without
// the members, or one that is not JSON, prints nothing.
func TestPrintAttestations(t *testing.T) {
	body := []byte(`{"attestation_outcome":{"outcome":"failed","failure":{"code":"ingest.attestation_missing","scope":"artifact","constraint":"link_downgraded","requirement":0},"evaluated_at":"2026-11-27T09:00:00Z"},
		"attestations":[{"digest":"sha256:aa","predicate_type":"p","verdict":"verified","signer":{"instance":"github","issuer":"iss","identity":"a\u202eb"},"attested_at":"2026-11-27T08:00:00Z"},
		{"digest":"sha256:bb","predicate_type":"p","verdict":"unverified"}]}`)
	var out bytes.Buffer
	printAttestations(&out, body)
	want := `attestation_outcome: "failed" code="ingest.attestation_missing" scope="artifact" constraint="link_downgraded" requirement=0 evaluated_at="2026-11-27T09:00:00Z"
attestation: "sha256:aa" "verified" predicate_type="p" instance="github" issuer="iss" identity="a\u202eb" attested_at="2026-11-27T08:00:00Z"
attestation: "sha256:bb" "unverified" predicate_type="p"
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	for _, b := range []string{`{}`, `not json`, `{"attestation_outcome":{"outcome":"verified"},"attestations":"x"}`} {
		out.Reset()
		printAttestations(&out, []byte(b))
		if b != `{"attestation_outcome":{"outcome":"verified"},"attestations":"x"}` && out.Len() != 0 {
			t.Errorf("%s printed %q", b, out.String())
		}
	}
	if out.String() != "attestation_outcome: \"verified\"\n" {
		t.Errorf("outcome alone printed %q", out.String())
	}
	if nullAsAbsent([]byte("null")) != nil || string(nullAsAbsent([]byte("[]"))) != "[]" {
		t.Error("nullAsAbsent")
	}
}
