package server

import (
	"slices"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.6 — a user-defined layer's owner and its implicit users:[owner]
// visibility are fixed at registration and cannot be widened.
// Spec: §7.3.1 — the immutable visibility rule's predicate: a field is
// asserted by a value that differs from what the layer stores, the comparison
// is exact, and an admin-defined layer asserts nothing whatever the values.
func TestAssertedImmutableVisibilityFields(t *testing.T) {
	t.Parallel()
	userDefined := store.LayerConfig{
		ID: "personal", UserDefined: true, Owner: "alice", Users: []string{"alice"},
	}
	adminDefined := store.LayerConfig{
		ID: "team", Owner: "alice", Users: []string{"alice"},
	}
	multiUser := store.LayerConfig{
		ID: "personal", UserDefined: true, Owner: "alice", Users: []string{"alice", "bob"},
	}

	cases := []struct {
		name  string
		patch LayerRegisterRequest
		cfg   store.LayerConfig
		want  []string
	}{
		{"groups alone", LayerRegisterRequest{Groups: []string{"acme-eng"}}, userDefined, []string{"groups"}},
		{"organization alone", LayerRegisterRequest{Organization: true}, userDefined, []string{"organization"}},
		{"owner alone", LayerRegisterRequest{Owner: "bob"}, userDefined, []string{"owner"}},
		{"public alone", LayerRegisterRequest{Public: true}, userDefined, []string{"public"}},
		{"users alone", LayerRegisterRequest{Users: []string{"bob"}}, userDefined, []string{"users"}},
		{
			"every field, sorted",
			LayerRegisterRequest{
				Groups: []string{"acme-eng"}, Organization: true, Owner: "bob",
				Public: true, Users: []string{"bob"},
			},
			userDefined,
			[]string{"groups", "organization", "owner", "public", "users"},
		},
		{"the zero value asserts nothing", LayerRegisterRequest{Ref: "release"}, userDefined, nil},
		{"public false", LayerRegisterRequest{Public: false}, userDefined, nil},
		{"organization false", LayerRegisterRequest{Organization: false}, userDefined, nil},
		{"empty groups", LayerRegisterRequest{Groups: []string{}}, userDefined, nil},
		{"empty users", LayerRegisterRequest{Users: []string{}}, userDefined, nil},
		{"empty owner", LayerRegisterRequest{Owner: ""}, userDefined, nil},
		{"owner restating the stored owner", LayerRegisterRequest{Owner: "alice"}, userDefined, nil},
		{"users restating the stored users", LayerRegisterRequest{Users: []string{"alice"}}, userDefined, nil},
		// The comparison is byte for byte, because an admitted owner is
		// stored verbatim and cfg.Owner bounds authorizeLayerWrite and the
		// per-identity user-defined layer cap, both of which compare exactly.
		{"owner padded with whitespace", LayerRegisterRequest{Owner: " alice "}, userDefined, []string{"owner"}},
		// The comparison is element for element, so a reordered users
		// differs from the stored value and asserts.
		{
			"users differing only in element order",
			LayerRegisterRequest{Users: []string{"bob", "alice"}},
			multiUser,
			[]string{"users"},
		},
		{"users restating a multi-element stored users", LayerRegisterRequest{Users: []string{"alice", "bob"}}, multiUser, nil},
		{
			"every field against an admin-defined layer",
			LayerRegisterRequest{
				Groups: []string{"acme-eng"}, Organization: true, Owner: "bob",
				Public: true, Users: []string{"bob"},
			},
			adminDefined,
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := assertedImmutableVisibilityFields(tc.patch, tc.cfg); !slices.Equal(got, tc.want) {
				t.Errorf("assertedImmutableVisibilityFields = %v, want %v", got, tc.want)
			}
		})
	}
}
