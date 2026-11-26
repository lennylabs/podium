package layer_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
)

// trustedHeadersCaller maps the §6.3.3 trusted-headers identity onto the
// evaluator's caller the way serverboot's trustedHeadersVerifier does, so the
// caller classes below include the identity that provider actually produces.
func trustedHeadersCaller(sub, email, groups string) layer.Identity {
	id := identity.IdentityFromTrustedHeaders(sub, email, groups, "", "", "")
	return layer.Identity{
		Sub:             id.Sub,
		Email:           id.Email,
		Groups:          id.Groups,
		IsAuthenticated: id.IsAuthenticated,
	}
}

// Spec: §4.6 — `groups` admits members of the listed groups and `users` admits
// the listed identifiers matched against the caller's subject or email, and
// public visibility is the only field that reaches an unauthenticated caller.
// Spec: §6.3.3 — a request carrying no credential, and a trusted-headers
// request without identity headers, is anonymous and sees public visibility
// only. An anonymous caller has an empty subject, so an empty `users` entry,
// an empty `groups` entry, or an empty member name a group resolver returns
// must admit no caller, and a non-empty one must not admit an anonymous caller
// whatever identifiers it carries.
func TestVisibleWith_CallerClassesAgainstEmptyAndNonEmptyEntries(t *testing.T) {
	t.Parallel()
	// The resolver stands in for the §6.3.1 SCIM directory. "ghosts" models a
	// directory that returns an empty member name.
	directory := map[string][]string{
		"ghosts":      {""},
		"engineering": {"alice", "bob@acme.com"},
	}
	resolve := func(g string) []string { return directory[g] }

	callers := map[string]layer.Identity{
		"anonymous":                {},
		"anonymous carrying names": {Sub: "alice", Email: "alice@acme.com", Groups: []string{"finance"}},
		"anonymous with empty group": {
			Groups: []string{""},
		},
		"authenticated empty subject and email": {IsAuthenticated: true},
		"authenticated empty group":             {IsAuthenticated: true, Groups: []string{""}},
		"authenticated email only":              {Email: "bob@acme.com", IsAuthenticated: true},
		"authenticated subject only":            {Sub: "alice", IsAuthenticated: true},
		"authenticated group member":            {Sub: "carol", IsAuthenticated: true, Groups: []string{"finance"}},
		"trusted headers without identity":      trustedHeadersCaller("", "alice@acme.com", "finance"),
		"trusted headers subject only":          trustedHeadersCaller("alice", "", ""),
		"trusted headers group member":          trustedHeadersCaller("carol", "", "finance"),
		"public mode":                           {IsPublic: true},
	}

	layers := map[string]layer.Visibility{
		"none":                 {},
		"public":               {Public: true},
		"organization":         {Organization: true},
		"empty user":           {Users: []string{""}},
		"empty group":          {Groups: []string{""}},
		"resolver empty name":  {Groups: []string{"ghosts"}},
		"user alice":           {Users: []string{"alice"}},
		"user alice email":     {Users: []string{"alice@acme.com"}},
		"user bob email":       {Users: []string{"bob@acme.com"}},
		"group finance":        {Groups: []string{"finance"}},
		"group via resolver":   {Groups: []string{"engineering"}},
		"empty then non-empty": {Users: []string{"", "alice"}},
	}

	// visible lists, per caller, the layers it sees. Every pair not listed is
	// asserted invisible.
	visible := map[string][]string{
		"anonymous":                             {"public"},
		"anonymous carrying names":              {"public"},
		"anonymous with empty group":            {"public"},
		"authenticated empty subject and email": {"public", "organization"},
		"authenticated empty group":             {"public", "organization"},
		"authenticated email only":              {"public", "organization", "user bob email", "group via resolver"},
		"authenticated subject only":            {"public", "organization", "user alice", "group via resolver", "empty then non-empty"},
		"authenticated group member":            {"public", "organization", "group finance"},
		"trusted headers without identity":      {"public"},
		"trusted headers subject only":          {"public", "organization", "user alice", "group via resolver", "empty then non-empty"},
		"trusted headers group member":          {"public", "organization", "group finance"},
		"public mode": {
			"none", "public", "organization", "empty user", "empty group", "resolver empty name",
			"user alice", "user alice email", "user bob email", "group finance", "group via resolver",
			"empty then non-empty",
		},
	}

	for callerName, caller := range callers {
		want := map[string]bool{}
		for _, l := range visible[callerName] {
			want[l] = true
		}
		for layerName, vis := range layers {
			l := layer.Layer{ID: layerName, Visibility: vis}
			if got := layer.VisibleWith(l, caller, resolve); got != want[layerName] {
				t.Errorf("%s / %s: VisibleWith = %v, want %v", callerName, layerName, got, want[layerName])
			}
		}
	}
}

// Spec: §4.6, §6.3.3 — the reproduction of the reported defect: a layer whose
// `users` holds an empty entry is visible to no anonymous caller, through
// Visible and through EffectiveLayers, and a group resolver that returns an
// empty member name admits no anonymous caller.
func TestVisible_EmptyEntryAdmitsNoAnonymousCaller(t *testing.T) {
	t.Parallel()
	blank := layer.Layer{ID: "blank", Visibility: layer.Visibility{Users: []string{""}}}
	if layer.Visible(blank, layer.Identity{}) {
		t.Errorf(`users: [""] is visible to an anonymous caller`)
	}
	pub := layer.Layer{ID: "pub", Precedence: 1, Visibility: layer.Visibility{Public: true}}
	got := layer.EffectiveLayers([]layer.Layer{blank, pub}, layer.Identity{})
	if len(got) != 1 || got[0].ID != "pub" {
		t.Errorf("anonymous effective layers = %v, want [pub]", got)
	}

	calls := 0
	resolve := func(string) []string { calls++; return []string{""} }
	ghosts := layer.Layer{ID: "ghosts", Visibility: layer.Visibility{Groups: []string{"ghosts"}}}
	if layer.VisibleWith(ghosts, layer.Identity{}, resolve) {
		t.Errorf("a resolver returning an empty member admits an anonymous caller")
	}
	if calls != 0 {
		t.Errorf("resolver called %d times for an anonymous caller; want 0", calls)
	}
	if layer.VisibleWith(ghosts, layer.Identity{Sub: "alice", IsAuthenticated: true}, resolve) {
		t.Errorf("a resolver returning an empty member admits a caller with an empty email")
	}
}
