package server

import (
	"reflect"
	"slices"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// visBool and visList build the presence-carrying members of a visibilityPatch
// the way decodeVisibilityPatch does for a body that carried them.
func visBool(b bool) *bool { return &b }

func visList(v []string) *[]string { return &v }

// Spec: §7.3.1 — the patch semantics on the visibility members: a member the
// body carried is applied, including at its zero value, a member it omitted
// keeps the stored value, JSON null is the member's empty value, and a
// carried list that decodes empty is stored as nil so the emptied list and
// the never-populated one are one stored value.
// Spec: §4.6 — withdrawing every member leaves a record setting no field.
func TestVisibilityPatch_ApplyTo(t *testing.T) {
	t.Parallel()
	granted := store.LayerConfig{
		ID: "team", Public: true, Organization: true,
		Groups: []string{"acme-eng"}, Users: []string{"alice"},
	}
	bare := store.LayerConfig{ID: "team"}

	cases := []struct {
		name string
		vis  visibilityPatch
		cfg  store.LayerConfig
		want store.LayerConfig
	}{
		{
			name: "an absent member preserves every axis",
			vis:  visibilityPatch{},
			cfg:  granted,
			want: granted,
		},
		{
			name: "public false withdraws a stored public",
			vis:  visibilityPatch{public: visBool(false)},
			cfg:  granted,
			want: store.LayerConfig{ID: "team", Organization: true, Groups: []string{"acme-eng"}, Users: []string{"alice"}},
		},
		{
			name: "organization false withdraws a stored organization",
			vis:  visibilityPatch{organization: visBool(false)},
			cfg:  granted,
			want: store.LayerConfig{ID: "team", Public: true, Groups: []string{"acme-eng"}, Users: []string{"alice"}},
		},
		{
			name: "an empty groups empties the stored list as nil",
			vis:  visibilityPatch{groups: visList([]string{})},
			cfg:  granted,
			want: store.LayerConfig{ID: "team", Public: true, Organization: true, Users: []string{"alice"}},
		},
		{
			// json.Unmarshal of null leaves the target at its zero value, so
			// a member carrying null reaches applyTo as a nil list and takes
			// the same normalization as [].
			name: "a null groups behaves as an empty one",
			vis:  visibilityPatch{groups: visList(nil)},
			cfg:  granted,
			want: store.LayerConfig{ID: "team", Public: true, Organization: true, Users: []string{"alice"}},
		},
		{
			name: "an empty users empties the stored list as nil",
			vis:  visibilityPatch{users: visList([]string{})},
			cfg:  granted,
			want: store.LayerConfig{ID: "team", Public: true, Organization: true, Groups: []string{"acme-eng"}},
		},
		{
			name: "a member restating the stored value writes it back",
			vis: visibilityPatch{
				public: visBool(true), organization: visBool(true),
				groups: visList([]string{"acme-eng"}), users: visList([]string{"alice"}),
			},
			cfg:  granted,
			want: granted,
		},
		{
			name: "every member withdrawn leaves a record setting no field",
			vis: visibilityPatch{
				public: visBool(false), organization: visBool(false),
				groups: visList([]string{}), users: visList([]string{}),
			},
			cfg:  granted,
			want: bare,
		},
		{
			name: "a present member grants on a record setting no field",
			vis:  visibilityPatch{public: visBool(true), groups: visList([]string{"acme-eng"})},
			cfg:  bare,
			want: store.LayerConfig{ID: "team", Public: true, Groups: []string{"acme-eng"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// reflect.DeepEqual separates a nil slice from an empty one,
			// which is the normalization this table exists to pin.
			if got := tc.vis.applyTo(tc.cfg); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("applyTo = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Spec: §4.6 — a user-defined layer's owner and its implicit users:[owner]
// visibility are fixed at registration and cannot be widened.
// Spec: §7.3.1 — the immutable visibility rule's predicate: a field is
// asserted by the value the patch would store when that value differs from
// what the layer stores, the comparison is exact, and an admin-defined layer
// asserts nothing whatever the values.
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
		vis   visibilityPatch
		cfg   store.LayerConfig
		want  []string
	}{
		{"groups alone", LayerRegisterRequest{}, visibilityPatch{groups: visList([]string{"acme-eng"})}, userDefined, []string{"groups"}},
		{"organization alone", LayerRegisterRequest{}, visibilityPatch{organization: visBool(true)}, userDefined, []string{"organization"}},
		{"owner alone", LayerRegisterRequest{Owner: "bob"}, visibilityPatch{}, userDefined, []string{"owner"}},
		{"public alone", LayerRegisterRequest{}, visibilityPatch{public: visBool(true)}, userDefined, []string{"public"}},
		{"users alone", LayerRegisterRequest{}, visibilityPatch{users: visList([]string{"bob"})}, userDefined, []string{"users"}},
		{
			"every field, sorted",
			LayerRegisterRequest{Owner: "bob"},
			visibilityPatch{
				public: visBool(true), organization: visBool(true),
				groups: visList([]string{"acme-eng"}), users: visList([]string{"bob"}),
			},
			userDefined,
			[]string{"groups", "organization", "owner", "public", "users"},
		},
		{"the zero value asserts nothing", LayerRegisterRequest{Ref: "release"}, visibilityPatch{}, userDefined, nil},
		// The three axes the class stores at their zero value: a carried
		// zero equals the stored value and asserts nothing.
		{"public false", LayerRegisterRequest{}, visibilityPatch{public: visBool(false)}, userDefined, nil},
		{"organization false", LayerRegisterRequest{}, visibilityPatch{organization: visBool(false)}, userDefined, nil},
		{"empty groups", LayerRegisterRequest{}, visibilityPatch{groups: visList([]string{})}, userDefined, nil},
		{"null groups", LayerRegisterRequest{}, visibilityPatch{groups: visList(nil)}, userDefined, nil},
		// Users is the one axis carrying a non-zero stored value, so
		// emptying it differs from the stored ["alice"] and asserts. §4.6
		// fixes against emptying it on the same footing as widening it.
		{"empty users", LayerRegisterRequest{}, visibilityPatch{users: visList([]string{})}, userDefined, []string{"users"}},
		{"null users", LayerRegisterRequest{}, visibilityPatch{users: visList(nil)}, userDefined, []string{"users"}},
		{"empty owner", LayerRegisterRequest{Owner: ""}, visibilityPatch{}, userDefined, nil},
		{"owner restating the stored owner", LayerRegisterRequest{Owner: "alice"}, visibilityPatch{}, userDefined, nil},
		{"users restating the stored users", LayerRegisterRequest{}, visibilityPatch{users: visList([]string{"alice"})}, userDefined, nil},
		// The comparison is byte for byte, because an admitted owner is
		// stored verbatim and cfg.Owner bounds authorizeLayerWrite and the
		// per-identity user-defined layer cap, both of which compare exactly.
		{"owner padded with whitespace", LayerRegisterRequest{Owner: " alice "}, visibilityPatch{}, userDefined, []string{"owner"}},
		// The comparison is element for element, so a reordered users
		// differs from the stored value and asserts.
		{
			"users differing only in element order",
			LayerRegisterRequest{},
			visibilityPatch{users: visList([]string{"bob", "alice"})},
			multiUser,
			[]string{"users"},
		},
		{
			"users restating a multi-element stored users",
			LayerRegisterRequest{},
			visibilityPatch{users: visList([]string{"alice", "bob"})},
			multiUser,
			nil,
		},
		{
			"every field against an admin-defined layer",
			LayerRegisterRequest{Owner: "bob"},
			visibilityPatch{
				public: visBool(true), organization: visBool(true),
				groups: visList([]string{"acme-eng"}), users: visList([]string{"bob"}),
			},
			adminDefined,
			nil,
		},
		{
			"a withdrawal against an admin-defined layer",
			LayerRegisterRequest{},
			visibilityPatch{public: visBool(false), users: visList([]string{})},
			adminDefined,
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := assertedImmutableVisibilityFields(tc.patch, tc.vis, tc.cfg); !slices.Equal(got, tc.want) {
				t.Errorf("assertedImmutableVisibilityFields = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spec: §7.3.1 — the presence decode reads the four visibility members off
// the body, so an absent key and a carried zero value are apart.
func TestDecodeVisibilityPatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want visibilityPatch
	}{
		{"an empty object carries no member", `{}`, visibilityPatch{}},
		{
			"a body carrying every member at its zero value",
			`{"public":false,"organization":false,"groups":[],"users":[]}`,
			visibilityPatch{
				public: visBool(false), organization: visBool(false),
				groups: visList([]string{}), users: visList([]string{}),
			},
		},
		{
			"null decodes to the member's empty value",
			`{"groups":null,"users":null}`,
			visibilityPatch{groups: visList(nil), users: visList(nil)},
		},
		{
			"a body carrying only unrelated members",
			`{"ref":"release","owner":"alice"}`,
			visibilityPatch{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeVisibilityPatch([]byte(tc.body))
			if err != nil {
				t.Fatalf("decodeVisibilityPatch: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decodeVisibilityPatch = %+v, want %+v", got, tc.want)
			}
		})
	}

	// A body reaching the handler has already passed the
	// LayerRegisterRequest decode, so these bodies are refused there first;
	// the branches are pinned here because the decoder is reachable on its
	// own and must fail closed rather than drop the member silently.
	t.Run("a malformed body is refused", func(t *testing.T) {
		t.Parallel()
		bodies := map[string]string{
			"not an object":  `["public"]`,
			"public":         `{"public":"yes"}`,
			"organization":   `{"organization":1}`,
			"groups":         `{"groups":"acme-eng"}`,
			"users":          `{"users":{}}`,
			"a list element": `{"groups":[1]}`,
		}
		for name, body := range bodies {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if _, err := decodeVisibilityPatch([]byte(body)); err == nil {
					t.Errorf("decodeVisibilityPatch admitted %s", body)
				}
			})
		}
	})
}
