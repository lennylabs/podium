package e2e

// End-to-end coverage of the §7.3.1 immutable visibility rule through the
// compiled binary: `podium layer update` asserting owner or a visibility field
// against a stored user-defined layer is refused with 400
// registry.invalid_argument carrying details.constraint
// "immutable_visibility", whoever the caller is.
//
// The rule reads the stored layer's class rather than the requesting caller,
// which is what separates it from the three neighbouring §7.3.1 rules. The
// arms below pin that separation where it is observable from outside the
// process: the owner and a tenant admin meet the same 400, a caller with no
// write right on the layer keeps the 403 the write rule answers first, and a
// registry started in public mode refuses on the same terms rather than
// admitting every caller the way its neighbours do there.
//
// The authenticated arms use the injected-session-token harness
// (startAuthServer), which boots the registry as a subprocess on every
// platform. The oidc-jwt stack skips on darwin; this file must not acquire a
// dependency on it, and the guard in assertAuthStackRan fails loudly rather
// than passing quietly if the harness ever stops authenticating callers here.
//
// Spec: §7.3.1 (the immutable visibility rule), §4.6 (a user-defined layer's
// implicit users:[<owner>] visibility is set automatically and cannot be
// widened), §13.10 (public mode).

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// immutableRepo is a network git URL, so the local-source rule, which precedes
// the immutable visibility rule on both endpoints, refuses no arm below.
const immutableRepo = "https://github.com/acme/notes.git"

// listedLayer is the subset of a layer record `podium layer list` prints that
// the assertions here read.
type listedLayer struct {
	ID           string   `json:"id"`
	UserDefined  bool     `json:"user_defined"`
	Owner        string   `json:"owner"`
	Public       bool     `json:"public"`
	Organization bool     `json:"organization"`
	Groups       []string `json:"groups"`
	Users        []string `json:"users"`
}

// cliLayer runs `podium layer list` in env and returns the record with id,
// reporting whether the caller sees it at all. The list is visibility-filtered
// per §4.6, so the second return value is the assertion a test makes about a
// caller's view rather than an error.
func cliLayer(t *testing.T, env []string, id string) (listedLayer, bool) {
	t.Helper()
	res := runPodium(t, "", env, "layer", "list")
	cliWantExit(t, res, 0, "layer list")
	var listed struct {
		Layers []listedLayer `json:"layers"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &listed); err != nil {
		t.Fatalf("decode layer list: %v\nstdout: %s", err, res.Stdout)
	}
	for _, l := range listed.Layers {
		if l.ID == id {
			return l, true
		}
	}
	return listedLayer{}, false
}

// assertRefusedImmutable asserts that res is the refusal the immutable
// visibility rule writes: a non-zero exit whose stderr carries the status, the
// §6.10 code, and the constraint that discriminates this refusal from the
// other two the endpoint answers on registry.invalid_argument. A bare non-zero
// exit does not tell an operator which rule refused, and the coded envelope is
// what a client branches on.
func assertRefusedImmutable(t *testing.T, res cliResult, what string) {
	t.Helper()
	cliWantNonZero(t, res, what)
	for _, want := range []string{"400", "registry.invalid_argument", "immutable_visibility"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("%s: stderr does not carry %q\nstderr: %s", what, want, res.Stderr)
		}
	}
}

// assertAuthStackRan confirms the harness authenticated the caller the token
// was minted for, by reading a layer only that caller can see. The e2e suite
// has a history of arms that skip silently on darwin and report a pass; this
// stack does not skip, and the check turns a future degradation into a failure
// here rather than into an assertion that never ran.
func assertAuthStackRan(t *testing.T, env []string, id, owner string) {
	t.Helper()
	rec, seen := cliLayer(t, env, id)
	if !seen {
		t.Fatalf("the layer %q is not in its owner's list; the harness did not authenticate %s, so the arms below would assert nothing", id, owner)
	}
	if !rec.UserDefined || rec.Owner != owner {
		t.Fatalf("the layer %q is user_defined=%v owner=%q, want a layer owned by %s; the refusal under test reads the stored class",
			id, rec.UserDefined, rec.Owner, owner)
	}
}

// Spec: §7.3.1 / §4.6 — a patch asserting owner or a visibility field against
// a stored user-defined layer is refused, and the refusal binds every caller.
// The owner holds every right on the layer and a tenant admin is admitted on
// the layer's write arm entirely, so both meeting the same 400 is what a 403
// reading would get wrong. A caller with no write right keeps the write rule's
// 403, which pins the precedence between the two rules through the binary.
//
// The recourse the spec documents is re-registration: an admin registers the
// ID as an admin-defined layer with the visibility they want, which is
// exercised here rather than asserted, through a third caller's view of the
// result.
func TestLayerCLI_ImmutableVisibilityRefusedForEveryCaller(t *testing.T) {
	t.Parallel()
	srv := startAuthServer(t, authServerSpec{
		BootstrapAdmins: []string{"alice@acme.com"},
		Layers: []authLayer{{
			ID:         "seed",
			Files:      map[string]string{"seed/note/ARTIFACT.md": authContext("seed note")},
			Visibility: authVisibility{Public: true},
		}},
	})
	adminEnv := acliEnv(t, srv, srv.adminToken("alice@acme.com"))
	bobEnv := acliEnv(t, srv, srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"}))
	carolEnv := acliEnv(t, srv, srv.token(authIdentity{Sub: "carol@acme.com", Email: "carol@acme.com"}))

	// ---- Bob registers a personal layer -------------------------------------
	reg := runPodium(t, "", bobEnv,
		"layer", "register", "--id", "bob-notes", "--repo", immutableRepo, "--ref", "main", "--user-defined")
	cliWantExit(t, reg, 0, "bob registers a user-defined layer")
	assertAuthStackRan(t, bobEnv, "bob-notes", "bob@acme.com")

	// ---- The owner's own widening is refused --------------------------------
	assertRefusedImmutable(t,
		runPodium(t, "", bobEnv, "layer", "update", "--id", "bob-notes", "--public"),
		"the owner asserting --public on their own layer")

	// ---- A tenant admin's identical invocation is refused identically -------
	assertRefusedImmutable(t,
		runPodium(t, "", adminEnv, "layer", "update", "--id", "bob-notes", "--public"),
		"a tenant admin asserting --public on a user-defined layer")

	// ---- A caller with no write right keeps the write rule's envelope -------
	// The write rule runs first, so carol never reaches the immutable
	// visibility rule and reads the 403 that names her missing right.
	carol := runPodium(t, "", carolEnv, "layer", "update", "--id", "bob-notes", "--public")
	cliWantNonZero(t, carol, "a third caller asserting --public on another user's layer")
	for _, want := range []string{"403", "auth.forbidden"} {
		if !strings.Contains(carol.Stderr, want) {
			t.Errorf("a third caller asserting --public: stderr does not carry %q\nstderr: %s", want, carol.Stderr)
		}
	}

	// ---- The record is unchanged --------------------------------------------
	rec, seen := cliLayer(t, bobEnv, "bob-notes")
	if !seen {
		t.Fatalf("the layer bob-notes left its owner's list after three refused patches")
	}
	if !rec.UserDefined || rec.Public || rec.Owner != "bob@acme.com" || !slices.Equal(rec.Users, []string{"bob@acme.com"}) {
		t.Errorf("after three refused patches bob-notes is %+v, want a user-defined layer at public:false owned by bob@acme.com with users:[bob@acme.com]", rec)
	}
	if _, carolSees := cliLayer(t, carolEnv, "bob-notes"); carolSees {
		t.Errorf("carol sees bob-notes; a refused widening left the layer visible beyond its owner")
	}

	// ---- The documented recourse: the admin re-registers the ID -------------
	rereg := runPodium(t, "", adminEnv,
		"layer", "register", "--id", "bob-notes", "--repo", immutableRepo, "--ref", "main", "--public")
	cliWantExit(t, rereg, 0, "the admin re-registers the ID as an admin-defined public layer")

	after, carolSees := cliLayer(t, carolEnv, "bob-notes")
	if !carolSees {
		t.Fatalf("carol does not see bob-notes after the admin re-registered it as a public layer")
	}
	if after.UserDefined || !after.Public {
		t.Errorf("re-registered bob-notes is %+v, want user_defined:false and public:true", after)
	}
}

// Spec: §7.3.1 / §13.10 — the rule binds a registry in public mode. Its three
// neighbouring §7.3.1 rules each go quiet there, because they read the caller
// and public mode authenticates none; this one reads the stored layer's class,
// so it refuses on the same terms. That is the one behaviour separating this
// rule from the neighbours it is otherwise written beside, so it is pinned
// through the binary rather than from the handler alone.
func TestLayerCLI_ImmutableVisibilityBindsPublicMode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/note/ARTIFACT.md": authContext("seed note")})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_INGEST_OFFLINE=true"},
		"serve", "--public-mode", "--layer-path", reg)
	var health map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health["mode"] != "public" {
		t.Fatalf("/healthz mode=%v, want public; the arm below would not be a public-mode assertion", health["mode"])
	}
	env := []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_TOKEN_KEYCHAIN_NAME=podium-immutable-visibility-test",
		"HOME=" + t.TempDir(),
	}

	// Public mode authenticates no caller, so the owner is named in the
	// request body; the layer is stored user-defined all the same.
	regRes := runPodium(t, "", env, "layer", "register", "--id", "dave-notes",
		"--repo", immutableRepo, "--ref", "main", "--user-defined", "--owner", "dave@acme.com")
	cliWantExit(t, regRes, 0, "a user-defined registration in public mode")
	rec, seen := cliLayer(t, env, "dave-notes")
	if !seen || !rec.UserDefined {
		t.Fatalf("dave-notes is %+v (seen=%v), want a stored user-defined layer", rec, seen)
	}

	assertRefusedImmutable(t,
		runPodium(t, "", env, "layer", "update", "--id", "dave-notes", "--public"),
		"asserting --public on a user-defined layer in public mode")

	// A patch that asserts none of the fixed fields still applies, so the
	// refusal above is the rule rather than a closed endpoint.
	cliWantExit(t, runPodium(t, "", env, "layer", "update", "--id", "dave-notes", "--ref", "release"),
		0, "patching a mutable field on the same layer")
	after, _ := cliLayer(t, env, "dave-notes")
	if after.Public {
		t.Errorf("dave-notes is public after a refused widening: %+v", after)
	}
}
