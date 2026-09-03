package identity

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// secondAudience is a second value the same registry answers to, the case a
// device-code token carrying the client identifier presents (§6.3.3).
const secondAudience = "podium-device-client"

// TestOIDCVerifier_AudienceSetAcceptance pins the disjunctive aud check: a
// token is accepted when its aud claim carries at least one configured
// audience, whichever position that value holds, and is rejected when it
// carries none.
// Spec: §6.3.3
func TestOIDCVerifier_AudienceSetAcceptance(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), []string{testAudience, secondAudience}, 300*time.Second)

	tests := []struct {
		name       string
		aud        any
		wantReject bool
	}{
		{"canonical entry", testAudience, false},
		{"later entry", secondAudience, false},
		{"unconfigured value", "https://other.example", true},
		{"array carrying one configured value", []any{"https://other.example", secondAudience}, false},
		{"array carrying no configured value", []any{"https://other.example", "https://third.example"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := validClaims(idp.issuer(), "")
			claims["aud"] = tc.aud
			_, err := v.Verify(idp.sign(t, "key-1", claims))
			if !tc.wantReject {
				if err != nil {
					t.Fatalf("Verify: %v, want acceptance", err)
				}
				return
			}
			var ute *UntrustedTokenError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %v (%T), want *UntrustedTokenError", err, err)
			}
		})
	}
}

// TestOIDCVerifier_AudienceClaimRequired pins that the aud claim stays required
// under a configured set: an absent claim, an empty string, an empty list, and
// a list holding one empty string are all rejected.
// Spec: §6.3.3
func TestOIDCVerifier_AudienceClaimRequired(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), []string{testAudience, secondAudience}, 300*time.Second)

	tests := []struct {
		name    string
		mutate  func(jwt.MapClaims)
		wantErr string
	}{
		{"absent", func(c jwt.MapClaims) { delete(c, "aud") }, ""},
		{"empty string", func(c jwt.MapClaims) { c["aud"] = "" }, ""},
		{"empty list", func(c jwt.MapClaims) { c["aud"] = []any{} }, ""},
		{"list of one empty string", func(c jwt.MapClaims) { c["aud"] = []any{""} }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := validClaims(idp.issuer(), testAudience)
			tc.mutate(claims)
			_, err := v.Verify(idp.sign(t, "key-1", claims))
			var ute *UntrustedTokenError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %v (%T), want *UntrustedTokenError", err, err)
			}
		})
	}
}

// TestNewOIDCVerifier_NormalizesAudiences pins the resolution rule: entries are
// trimmed, blanks are dropped, duplicates collapse to their first occurrence,
// the remaining order is preserved, the first entry is canonical, and the
// caller's slice is not aliased.
// Spec: §6.3.3
func TestNewOIDCVerifier_NormalizesAudiences(t *testing.T) {
	t.Parallel()
	in := []string{"  ", " https://a.example ", "https://b.example", "https://a.example", "", "\t"}
	v := NewOIDCVerifier("https://issuer.example", in, 300*time.Second)

	got := v.AcceptedAudiences()
	want := []string{"https://a.example", "https://b.example"}
	if len(got) != len(want) {
		t.Fatalf("AcceptedAudiences() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AcceptedAudiences() = %v, want %v", got, want)
		}
	}
	if v.CanonicalAudience() != want[0] {
		t.Errorf("CanonicalAudience() = %q, want %q", v.CanonicalAudience(), want[0])
	}

	// The caller's slice and the returned copy are both independent of the
	// verifier's set, so neither can widen what the registry accepts.
	in[1] = "https://evil.example"
	got[0] = "https://evil.example"
	if again := v.AcceptedAudiences(); again[0] != want[0] {
		t.Errorf("after mutating the inputs, AcceptedAudiences() = %v, want %v", again, want)
	}

	if empty := NewOIDCVerifier("https://issuer.example", nil, 300*time.Second); empty.CanonicalAudience() != "" {
		t.Errorf("CanonicalAudience() with no audience = %q, want empty", empty.CanonicalAudience())
	}
}

// TestNormalizeAudiences covers the exported helper directly, which
// internal/serverboot resolves the configured set through.
// Spec: §6.3.3
func TestNormalizeAudiences(t *testing.T) {
	t.Parallel()
	if got := NormalizeAudiences(nil); len(got) != 0 {
		t.Errorf("NormalizeAudiences(nil) = %v, want empty", got)
	}
	if got := NormalizeAudiences([]string{" ", "\t\n"}); len(got) != 0 {
		t.Errorf("all-blank = %v, want empty", got)
	}
	got := NormalizeAudiences([]string{"b", "a", "b"})
	if strings.Join(got, ",") != "b,a" {
		t.Errorf("NormalizeAudiences = %v, want [b a]", got)
	}
}

// TestOIDCVerifier_BlankAudienceEntryIsDropped pins that dropping blank entries
// during resolution is a security predicate. The JWT library rejects an aud
// claim that is absent, an empty string, an empty list, or a list holding one
// empty string, but a claim such as ["", "https://other.example"] falls through
// to a membership test that matches the token's empty entry against a
// configured empty one (golang-jwt/jwt/v5 Validator.verifyAudience). A verifier
// carrying a blank entry therefore admits a token no configured audience names,
// which is why the case builds one past the constructor and asserts the
// acceptance. Do not delete it as contrived: it is the reason the constructor's
// drop exists.
// Spec: §6.3.3
func TestOIDCVerifier_BlankAudienceEntryIsDropped(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	claims := validClaims(idp.issuer(), "")
	claims["aud"] = []any{"", "https://other.example"}
	token := idp.sign(t, "key-1", claims)

	admitting := NewOIDCVerifier(idp.issuer(), []string{testAudience}, 300*time.Second)
	admitting.audiences = []string{""} // past the constructor, as no operator input can produce
	if _, err := admitting.Verify(token); err != nil {
		t.Fatalf("a blank configured audience did not admit aud [\"\", ...]: %v; the constructor's drop is what forecloses this", err)
	}

	v := NewOIDCVerifier(idp.issuer(), []string{"", testAudience}, 300*time.Second)
	got := v.AcceptedAudiences()
	if len(got) != 1 || got[0] != testAudience {
		t.Fatalf("AcceptedAudiences() = %v, want [%s]", got, testAudience)
	}
	var ute *UntrustedTokenError
	if _, err := v.Verify(token); !errors.As(err, &ute) {
		t.Fatalf("err = %v (%T), want *UntrustedTokenError", err, err)
	}
}
