package identity_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lennylabs/podium/pkg/identity"
)

// runtimeAudience is the registry endpoint the runtime calls, and
// runtimeSecondAudience is a second value the same registry answers to
// (§6.3.3).
const (
	runtimeAudience       = "https://podium.acme.com"
	runtimeSecondAudience = "https://podium.internal.acme.com"
)

// runtimeAudienceRegistry returns a registry holding one RS256 runtime key
// under the issuer "rt", with the private key that signs its tokens.
func runtimeAudienceRegistry(t *testing.T) (*identity.RuntimeKeyRegistry, func(jwt.MapClaims) string) {
	t.Helper()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return reg, func(claims jwt.MapClaims) string {
		return signJWT(t, priv, jwt.SigningMethodRS256, claims)
	}
}

// runtimeClaims returns a §6.3.2 token body whose aud claim the caller sets.
func runtimeClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "rt", "sub": "alice", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}
}

// TestJWTVerifier_AudienceSetAcceptance mirrors the §6.3.3 acceptance table on
// the §6.3.2 runtime verifier: a token is accepted when its aud claim carries
// at least one configured audience, whichever position that value holds, and
// is rejected when it carries none.
// Spec: §6.3.2, §6.3.3
func TestJWTVerifier_AudienceSetAcceptance(t *testing.T) {
	t.Parallel()
	reg, sign := runtimeAudienceRegistry(t)
	verify := reg.JWTVerifier([]string{runtimeAudience, runtimeSecondAudience}, nil)

	tests := []struct {
		name       string
		aud        any
		wantReject bool
	}{
		{"canonical entry", runtimeAudience, false},
		{"later entry", runtimeSecondAudience, false},
		{"unconfigured value", "https://other.example", true},
		{"array carrying one configured value", []any{"https://other.example", runtimeSecondAudience}, false},
		{"array carrying no configured value", []any{"https://other.example", "https://third.example"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := runtimeClaims()
			claims["aud"] = tc.aud
			id, err := verify(sign(claims))
			if !tc.wantReject {
				if err != nil {
					t.Fatalf("verify: %v, want acceptance", err)
				}
				if id.Sub != "alice" {
					t.Errorf("Sub = %q, want alice", id.Sub)
				}
				return
			}
			if !errors.Is(err, identity.ErrUntrustedRuntime) {
				t.Fatalf("got %v, want ErrUntrustedRuntime", err)
			}
		})
	}
}

// TestJWTVerifier_UnconfiguredAudienceRefusesEveryToken pins the fail-closed
// rejection a set that resolves to no entry carries. Splatting an empty set
// into jwt.WithAudience would leave the validator's expected set empty and skip
// the aud check, so both a well-formed token and a token carrying no aud at all
// must be refused, under an empty set and under a set whose entries are all
// blank.
// Spec: §6.3.2, §6.3.3
func TestJWTVerifier_UnconfiguredAudienceRefusesEveryToken(t *testing.T) {
	t.Parallel()
	reg, sign := runtimeAudienceRegistry(t)

	sets := map[string][]string{
		"empty set":     nil,
		"all-blank set": {"", "   ", "\t"},
	}
	tokens := map[string]jwt.MapClaims{
		"well-formed token": func() jwt.MapClaims {
			c := runtimeClaims()
			c["aud"] = runtimeAudience
			return c
		}(),
		"token carrying no aud": runtimeClaims(),
	}
	for setName, audiences := range sets {
		for tokenName, claims := range tokens {
			t.Run(setName+"/"+tokenName, func(t *testing.T) {
				_, err := reg.JWTVerifier(audiences, nil)(sign(claims))
				if !errors.Is(err, identity.ErrUntrustedRuntime) {
					t.Fatalf("got %v, want ErrUntrustedRuntime", err)
				}
				if !strings.Contains(err.Error(), "audience") {
					t.Errorf("error should explain the audience is unconfigured, got %v", err)
				}
			})
		}
	}
}

// TestJWTVerifier_NormalizesAudiences pins that the verifier resolves its set
// through NormalizeAudiences: a blank entry is dropped rather than carried, so
// a token whose aud array holds an empty string beside an unconfigured value
// cannot match it, and surrounding whitespace on a configured entry does not
// prevent a match.
// Spec: §6.3.3
func TestJWTVerifier_NormalizesAudiences(t *testing.T) {
	t.Parallel()
	reg, sign := runtimeAudienceRegistry(t)
	verify := reg.JWTVerifier([]string{"", "  " + runtimeAudience + " "}, nil)

	claims := runtimeClaims()
	claims["aud"] = runtimeAudience
	if _, err := verify(sign(claims)); err != nil {
		t.Fatalf("verify a trimmed configured audience: %v", err)
	}

	blank := runtimeClaims()
	blank["aud"] = []any{"", "https://other.example"}
	if _, err := verify(sign(blank)); !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime for an aud carrying a blank entry", err)
	}
}
