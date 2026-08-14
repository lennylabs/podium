package identity

import (
	"slices"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// Spec: §6.3.1 — claimIdentity reads the subject and the group claim from the
// keys claimNames names, defaulting to "sub" and "groups". The subject read is
// exact and has no fallback, and the group claim is accepted in the array form
// and in the single-string form.
func TestClaimIdentity_ClaimNames(t *testing.T) {
	t.Parallel()

	const adfsGroups = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"

	cases := []struct {
		name       string
		claims     jwt.MapClaims
		names      claimNames
		wantSub    string
		wantGroups []string
		wantErr    string
	}{
		{
			name:       "default names",
			claims:     jwt.MapClaims{"sub": "alice", "groups": []any{"finance", "engineering"}},
			wantSub:    "alice",
			wantGroups: []string{"finance", "engineering"},
		},
		{
			name:    "default subject missing",
			claims:  jwt.MapClaims{"groups": []any{"finance"}},
			wantErr: "sub claim missing",
		},
		{
			name:    "configured subject claim",
			claims:  jwt.MapClaims{"idsub": "alice-pairwise"},
			names:   claimNames{Subject: "idsub"},
			wantSub: "alice-pairwise",
		},
		{
			// Decision 6: the configured claim is read alone, so a token that
			// carries sub and not the configured claim is rejected.
			name:    "configured subject claim absent does not fall back to sub",
			claims:  jwt.MapClaims{"sub": "alice"},
			names:   claimNames{Subject: "idsub"},
			wantErr: "idsub claim missing",
		},
		{
			name:    "configured subject claim of a non-string type",
			claims:  jwt.MapClaims{"idsub": 42},
			names:   claimNames{Subject: "idsub"},
			wantErr: "idsub claim missing",
		},
		{
			name:       "configured group claim in the array form",
			claims:     jwt.MapClaims{"sub": "alice", adfsGroups: []any{"acme-finance", "acme-eng"}},
			names:      claimNames{Groups: adfsGroups},
			wantSub:    "alice",
			wantGroups: []string{"acme-finance", "acme-eng"},
		},
		{
			// A caller in exactly one group receives the claim as a plain
			// string. The whole value is one group name and is not split.
			name:       "configured group claim in the single-string form",
			claims:     jwt.MapClaims{"sub": "alice", adfsGroups: "acme-finance,acme-eng"},
			names:      claimNames{Groups: adfsGroups},
			wantSub:    "alice",
			wantGroups: []string{"acme-finance,acme-eng"},
		},
		{
			name:       "default group claim in the single-string form",
			claims:     jwt.MapClaims{"sub": "alice", "groups": "finance"},
			wantSub:    "alice",
			wantGroups: []string{"finance"},
		},
		{
			name:    "configured group claim is read alone",
			claims:  jwt.MapClaims{"sub": "alice", "groups": []any{"finance"}},
			names:   claimNames{Groups: adfsGroups},
			wantSub: "alice",
		},
		{
			name:       "group claim elements of other types are skipped",
			claims:     jwt.MapClaims{"sub": "alice", "groups": []any{"finance", 7, "", "engineering"}},
			wantSub:    "alice",
			wantGroups: []string{"finance", "engineering"},
		},
		{
			name:    "empty single-string group claim yields no group",
			claims:  jwt.MapClaims{"sub": "alice", "groups": ""},
			wantSub: "alice",
		},
		{
			name:    "group claim of an unsupported type yields no group",
			claims:  jwt.MapClaims{"sub": "alice", "groups": 7},
			wantSub: "alice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := claimIdentity(tc.claims, tc.names)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if id.IsAuthenticated || id.Sub != "" {
					t.Errorf("Identity on error = %+v, want the zero value", id)
				}
				return
			}
			if err != nil {
				t.Fatalf("claimIdentity: %v", err)
			}
			if id.Sub != tc.wantSub {
				t.Errorf("Sub = %q, want %q", id.Sub, tc.wantSub)
			}
			if !id.IsAuthenticated {
				t.Error("IsAuthenticated = false, want true")
			}
			if !slices.Equal(id.Groups, tc.wantGroups) {
				t.Errorf("Groups = %v, want %v", id.Groups, tc.wantGroups)
			}
		})
	}
}

// Spec: §6.3.1 — the email, org_id, and scope derivation is shared by both JWT
// verifiers and is unaffected by the subject and group claim names.
func TestClaimIdentity_DerivesEmailOrgAndScopes(t *testing.T) {
	t.Parallel()
	id, err := claimIdentity(jwt.MapClaims{
		"idsub":  "alice-pairwise",
		"email":  "alice@acme.com",
		"org_id": "acme",
		"scope":  "podium:read:finance/*",
		"scp":    []any{"podium:load:finance/ap/pay-invoice@1.x"},
	}, claimNames{Subject: "idsub", Groups: "roles"})
	if err != nil {
		t.Fatalf("claimIdentity: %v", err)
	}
	if id.Email != "alice@acme.com" || id.OrgID != "acme" {
		t.Errorf("Identity = %+v", id)
	}
	want := []string{"podium:read:finance/*", "podium:load:finance/ap/pay-invoice@1.x"}
	if !slices.Equal(id.Scopes, want) {
		t.Errorf("Scopes = %v, want %v", id.Scopes, want)
	}
}
