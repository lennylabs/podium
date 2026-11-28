package store

import (
	"strings"
	"testing"
	"time"
)

// Spec: §4.7.11 (Attestation outcome, Attestation storage) — the outcome
// comparison reads the failure and its requirement, a stored failure that
// does not decode is an error, the link order lists the verified links of a
// version first and then by key, and a time column scans from either
// backend's form.
func TestAttestationStoreHelpers(t *testing.T) {
	t.Parallel()
	one, two := 1, 2
	f := func(r *int) *AttestationFailure {
		return &AttestationFailure{Code: "c", Scope: "artifact", Requirement: r}
	}
	failed := func(x *AttestationFailure) VersionOutcome { return VersionOutcome{Outcome: OutcomeFailed, Failure: x} }
	var got []bool
	for _, p := range [][2]VersionOutcome{
		{failed(f(&one)), failed(f(&one))}, {failed(f(&one)), failed(f(&two))}, {failed(f(nil)), failed(f(&one))},
		{failed(f(nil)), failed(nil)}, {failed(nil), failed(nil)}, {{Outcome: OutcomeVerified}, failed(nil)},
	} {
		got = append(got, p[0].Equal(p[1]))
	}
	if want := "[true false false false true false]"; boolsText(got) != want {
		t.Errorf("Equal = %s, want %s", boolsText(got), want)
	}
	if o := normalizedOutcome(failed(f(&one))); o.Failure.Requirement == &one || *o.Failure.Requirement != 1 {
		t.Error("normalizedOutcome shares the requirement")
	}
	if _, err := parseFailureColumn("{"); err == nil {
		t.Error("a failure that does not decode")
	}
	links := []AttestationLink{
		{ArtifactID: "a", Version: "1", Verdict: VerdictVerified, PredicateType: "p", Instance: "i", SignerIssuer: "x", SignerIdentity: "2"},
		{ArtifactID: "a", Version: "1", Verdict: VerdictVerified, PredicateType: "p", Instance: "i", SignerIssuer: "x", SignerIdentity: "1"},
		{ArtifactID: "a", Version: "1", Verdict: VerdictVerified, PredicateType: "p", Instance: "i", SignerIssuer: "w", SignerIdentity: "9"},
		{ArtifactID: "a", Version: "1", Verdict: VerdictVerified, PredicateType: "p", Instance: "h", SignerIssuer: "z"},
		{ArtifactID: "a", Version: "1", Verdict: VerdictUnverified, PredicateType: "a"},
		{ArtifactID: "a", Version: "0", Verdict: VerdictUnverified},
	}
	sortLinks(links)
	var order []string
	for _, l := range links {
		order = append(order, l.Version+l.Instance+l.SignerIssuer+l.SignerIdentity)
	}
	if strings.Join(order, ",") != "0,1hz,1iw9,1ix1,1ix2,1" {
		t.Errorf("order %v", order)
	}
	var c timeCol
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	if c.Scan([]byte(at.Format(time.RFC3339Nano))) != nil || !c.t.Equal(at) || c.Scan(nil) != nil || !c.t.IsZero() ||
		c.Scan(7) == nil || c.Scan("not a time") == nil {
		t.Error("timeCol.Scan")
	}
	if got := (attSQL{pg: true}).q("a = ? AND b = ?"); got != "a = $1 AND b = $2" {
		t.Errorf("q = %s", got)
	}
}

// boolsText renders bools as a bracketed list.
func boolsText(b []bool) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = map[bool]string{true: "true", false: "false"}[v]
	}
	return "[" + strings.Join(parts, " ") + "]"
}
