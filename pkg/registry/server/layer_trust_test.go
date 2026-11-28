package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

const (
	trustIssuer   = "https://token.actions.githubusercontent.com"
	trustRepo     = "https://github.com/acme/finance"
	trustWorkflow = trustRepo + "/.github/workflows/podium-attest.yml@refs/heads/main"
)

// trustHarness serves a layer endpoint over a memory store with an audit sink
// and a publish recorder. caller is the identity every request resolves, and
// admin decides whether the admin arm admits it.
type trustHarness struct {
	base string
	st   store.Store
	sink *audit.FileSink
	rec  *eventRecorder
}

func newTrustHarness(t *testing.T, caller layer.Identity, admin bool, customize func(*server.LayerEndpoint) *server.LayerEndpoint) *trustHarness {
	t.Helper()
	h := &trustHarness{st: store.NewMemory(), sink: newAuditSink(t), rec: &eventRecorder{}}
	if err := h.st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(h.st, "t", server.NewModeTracker()).
		WithAudit(h.sink).
		WithEventPublisher(h.rec.publish).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return caller, nil }).
		WithAdminAuth(func(*http.Request) error {
			if admin {
				return nil
			}
			return server.ErrAdminRequired
		})
	if customize != nil {
		endpoint = customize(endpoint)
	}
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	h.base = ts.URL
	return h
}

// stored reads layer id from the store.
func (h *trustHarness) stored(t *testing.T, id string) store.LayerConfig {
	t.Helper()
	cfg, err := h.st.GetLayerConfig(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	return cfg
}

// lastAuditContext returns the context fields of the last audit record.
func (h *trustHarness) lastAuditContext(t *testing.T) map[string]string {
	t.Helper()
	lines := auditLines(t, h.sink)
	if len(lines) == 0 {
		t.Fatal("no audit record")
	}
	var ev struct {
		Context map[string]string `json:"context"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatalf("decode audit record: %v", err)
	}
	return ev.Context
}

// gitTrustBody is a registration of the git layer finance with policy
// members merged in.
func gitTrustBody(extra map[string]any) map[string]any {
	body := map[string]any{"id": "finance", "source_type": "git", "repo": trustRepo, "ref": "podium/signed"}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// ownSigner is the signer of the layer's own workflow with no binding.
func ownSigner() map[string]any {
	return map[string]any{"issuer": trustIssuer, "identity": trustWorkflow}
}

func requirement(signers ...map[string]any) []any {
	list := make([]any, len(signers))
	for i, s := range signers {
		list[i] = s
	}
	return []any{map[string]any{"signers": list}}
}

// Spec: §4.7.11 (Derived source binding), §7.3.1 (Layer trust policy) — a
// registration stores the policy it carries with the source binding derived
// from the layer's repository, and the layer object and the layer read report
// the derived members.
func TestLayerTrust_RegisterStoresDerivedBinding(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{
		"trust_mode": "require", "trust_requirements": requirement(ownSigner()),
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	want := store.TrustSigner{Issuer: trustIssuer, Identity: trustWorkflow, SourceRepository: trustRepo, SourceRepositoryRef: "refs/heads/main"}
	var reg struct {
		Layer store.LayerConfig `json:"layer"`
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := h.stored(t, "finance")
	for where, cfg := range map[string]store.LayerConfig{"response": reg.Layer, "store": got} {
		if cfg.TrustMode != "require" || len(cfg.TrustRequirements) != 1 || cfg.TrustRequirements[0].Signers[0] != want {
			t.Errorf("%s policy = %q %+v, want require with %+v", where, cfg.TrustMode, cfg.TrustRequirements, want)
		}
	}
	var list struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(mustGet(t, h.base, "/v1/layers"), &list); err != nil || len(list.Layers) != 1 ||
		list.Layers[0].TrustRequirements[0].Signers[0] != want {
		t.Errorf("GET /v1/layers = %+v, %v, want the derived member", list.Layers, err)
	}
}

// Spec: §4.7.11 (Policy rules), §7.3.1 (Layer trust policy) — a registration
// whose policy breaks a rule is refused with 400 registry.invalid_argument
// naming the rule, and stores no layer.
func TestLayerTrust_RegisterRefusals(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	unbound := map[string]any{"issuer": trustIssuer, "identity": "https://github.com/acme/.github/.github/workflows/a.yml@refs/heads/main"}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"mode", gitTrustBody(map[string]any{"trust_mode": "strict"}), `trust_mode "strict"`},
		{"several unmarked", gitTrustBody(map[string]any{"trust_requirements": append(requirement(ownSigner()), requirement(ownSigner())...)}), "0 of them set covers_layer"},
		{"optional covering", gitTrustBody(map[string]any{"trust_requirements": []any{map[string]any{"signers": []any{ownSigner()}, "optional": true}}}), "cannot be optional"},
		{"unknown type", gitTrustBody(map[string]any{"trust_mode": "warn", "trust_requirements": requirement(ownSigner()), "trust_require_types": []string{"hooks"}}), `"hooks" is not an artifact type`},
		{"types under none", gitTrustBody(map[string]any{"trust_mode": "none", "trust_requirements": requirement(ownSigner()), "trust_require_types": []string{"hook"}}), "trust_mode is none"},
		{"types not strings", gitTrustBody(map[string]any{"trust_require_types": []any{1}}), "trust_require_types"},
		{"types misspelled", gitTrustBody(map[string]any{"trust_require_type": []string{"hook"}}), `"trust_require_type" is not a trust policy member`},
		{"no requirement", gitTrustBody(map[string]any{"trust_mode": "require"}), "needs a requirement"},
		{"no signer", gitTrustBody(map[string]any{"trust_requirements": []any{map[string]any{"signers": []any{}}}}), "names no signer"},
		{"no binding", gitTrustBody(map[string]any{"trust_mode": "warn", "trust_requirements": requirement(unbound)}), "no source binding"},
		{"ssh derives nothing", map[string]any{"id": "finance", "source_type": "git", "repo": "git@github.com:acme/finance.git",
			"trust_mode": "require", "trust_requirements": requirement(ownSigner())}, "no source binding"},
		{"unknown instance", gitTrustBody(map[string]any{"trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": "alice@acme.com", "instance": "github"})}), `instance "github"`},
		{"root on git", gitTrustBody(map[string]any{"trust_layer_root": "skills"}), "applies to a local layer"},
		{"local root", map[string]any{"id": "finance", "source_type": "local", "local_path": t.TempDir(), "trust_layer_root": "a/../b"}, `segment ".."`},
		{"unknown member", gitTrustBody(map[string]any{"trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": "alice@acme.com", "instance_name": "x"})}), `unknown field "instance_name"`},
		{"covers_layer misspelled", gitTrustBody(map[string]any{"trust_requirements": []any{map[string]any{"signers": []any{ownSigner()}, "covers_layers": true}}}), `unknown field "covers_layers"`},
	} {
		resp, body := mustPost(t, h.base, "/v1/layers", tc.body)
		env := server.ErrorResponse{}
		_ = json.Unmarshal(body, &env)
		if resp.StatusCode != http.StatusBadRequest || env.Code != "registry.invalid_argument" || !strings.Contains(env.Message, tc.want) {
			t.Errorf("%s: %d %s, want 400 registry.invalid_argument with %q", tc.name, resp.StatusCode, body, tc.want)
		}
	}
	if layers, _ := h.st.ListLayerConfigs(context.Background(), "t"); len(layers) != 0 {
		t.Errorf("a refused registration stored %d layers", len(layers))
	}
	if _, err := os.Stat(h.sink.Path()); !os.IsNotExist(err) {
		t.Errorf("a refused registration wrote the audit log: %v", err)
	}
}

// Spec: §4.7.11 (Policy rules) — a signer may name an instance the registry
// configures, and a local layer stores its trust_layer_root.
func TestLayerTrust_ConfiguredInstanceAndLayerRoot(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithTrustInstances(func(n string) bool { return n == "github" })
	})
	resp, body := mustPost(t, h.base, "/v1/layers", map[string]any{
		"id": "local", "source_type": "local", "local_path": t.TempDir(), "trust_mode": "warn", "trust_layer_root": "skills/finance",
		"trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": "alice@acme.com", "instance": "github"}),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	got := h.stored(t, "local")
	if got.TrustLayerRoot != "skills/finance" || got.TrustRequirements[0].Signers[0].Instance != "github" {
		t.Errorf("stored policy = %+v root %q", got.TrustRequirements, got.TrustLayerRoot)
	}
}

// Spec: §7.3.1 (Patch semantics on the update body) — the trust members are
// applied by presence: an omitted member keeps the stored value, "" returns
// the mode to the default, and [] removes the requirement. A requirement the
// patch carries is derived against the stored repo.
func TestLayerTrust_UpdatePresenceSemantics(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	steps := []struct {
		body       map[string]any
		mode       string
		reqs, root int
	}{
		{map[string]any{"trust_mode": "require", "trust_requirements": requirement(ownSigner())}, "require", 1, 0},
		{map[string]any{"trust_mode": "warn"}, "warn", 1, 0},
		{map[string]any{"ref": "main"}, "warn", 1, 0},
		{map[string]any{"trust_mode": ""}, "", 1, 0},
		{map[string]any{"trust_requirements": []any{}}, "", 0, 0},
	}
	for i, step := range steps {
		resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", step.body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("step %d: %d %s", i, resp.StatusCode, body)
		}
		got := h.stored(t, "finance")
		if got.TrustMode != step.mode || len(got.TrustRequirements) != step.reqs {
			t.Errorf("step %d: mode %q with %d requirements, want %q with %d", i, got.TrustMode, len(got.TrustRequirements), step.mode, step.reqs)
		}
		if i == 0 && got.TrustRequirements[0].Signers[0].SourceRepository != trustRepo {
			t.Errorf("step 0: the update derived no binding: %+v", got.TrustRequirements)
		}
	}
	// A patch whose policy breaks a rule writes nothing, not even the other
	// members it carries.
	resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": "require", "ref": "release"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "needs a requirement") {
		t.Fatalf("invalid update: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "finance"); got.TrustMode != "" || got.Ref != "main" {
		t.Errorf("a refused update stored mode %q ref %q", got.TrustMode, got.Ref)
	}
	if resp, body := putRaw(t, h.base, "/v1/layers/update?id=finance", `{"trust_requirements":[{"signers":[],"x":1}]}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown requirement member: %d %s", resp.StatusCode, body)
	}
}

// Spec: §7.3.1 (Layer trust policy) — an update that carries no trust member
// does not check the stored policy, so a policy that a later configuration
// made invalid does not refuse an update of another member, while an update
// that carries a trust member is checked in full.
func TestLayerTrust_UpdateWithoutTrustMembersSkipsCheck(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	stale := store.LayerConfig{TenantID: "t", ID: "finance", SourceType: "git", Repo: trustRepo, Ref: "main", TrustMode: "require",
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: trustIssuer, Identity: "alice@acme.com", Instance: "removed"}}}}}
	if err := h.st.PutLayerConfig(context.Background(), stale); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"ref": "release"}); resp.StatusCode != http.StatusOK {
		t.Errorf("an update of another member: %d %s", resp.StatusCode, body)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": "warn"}); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(body), `instance \"removed\"`) {
		t.Errorf("a trust update over the stale policy: %d %s", resp.StatusCode, body)
	}
}

// Spec: §4.7.11 (Policy changes), §7.3.1 (The layer lifecycle event follows
// what the write changed) — a policy change records the previous and new
// policy in the layer's event and wakes no watcher; a change of another
// member records no trust fields; restating the policy records nothing; a
// re-registration records the replaced policy as the previous one.
func TestLayerTrust_PolicyChangeEvent(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	reg := gitTrustBody(map[string]any{"trust_mode": "require", "trust_requirements": requirement(ownSigner())})
	if resp, body := mustPost(t, h.base, "/v1/layers", reg); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	ctx := h.lastAuditContext(t)
	if ctx["trust_mode_before"] != "" || ctx["trust_mode"] != "require" || ctx["trust_requirements_before"] != "[]" ||
		!strings.Contains(ctx["trust_requirements"], `"source_repository_ref":"refs/heads/main"`) {
		t.Errorf("register event context = %v", ctx)
	}
	wakes := len(h.rec.typesFor())

	update := func(body map[string]any) {
		t.Helper()
		if resp, out := mustPut(t, h.base, "/v1/layers/update?id=finance", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("update %v: %d %s", body, resp.StatusCode, out)
		}
	}
	update(map[string]any{"trust_mode": "warn", "trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": "https://x.example/w", "any_source": true})})
	ctx = h.lastAuditContext(t)
	if ctx["action"] != "update" || ctx["trust_mode_before"] != "require" || ctx["trust_mode"] != "warn" ||
		!strings.Contains(ctx["trust_requirements"], `"any_source":true`) || !strings.Contains(ctx["trust_requirements_before"], trustWorkflow) {
		t.Errorf("update event context = %v", ctx)
	}
	if got := len(h.rec.typesFor()); got != wakes {
		t.Errorf("a trust policy change woke %d watchers", got-wakes)
	}
	lines := len(auditLines(t, h.sink))
	update(map[string]any{"trust_mode": "warn"})
	if got := len(auditLines(t, h.sink)); got != lines {
		t.Errorf("restating the policy recorded %d events", got-lines)
	}
	update(map[string]any{"ref": "main"})
	if ctx = h.lastAuditContext(t); ctx["trust_mode"] != "" || ctx["trust_mode_before"] != "" {
		t.Errorf("an update of another member recorded trust fields: %v", ctx)
	}
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-register: %d %s", resp.StatusCode, body)
	}
	if ctx = h.lastAuditContext(t); ctx["trust_mode_before"] != "warn" || ctx["trust_requirements"] != "[]" {
		t.Errorf("re-registration event context = %v", ctx)
	}
}

// Spec: §4.7.11 (Policy changes), §7.3.1 (Layer write authorization) — the
// policy of an admin-defined layer is changed by a tenant admin alone, and
// the owner of a user-defined layer sets and changes its own.
func TestLayerTrust_Authorization(t *testing.T) {
	t.Parallel()
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	h := newTrustHarness(t, alice, false, nil)
	admin := store.LayerConfig{TenantID: "t", ID: "org", SourceType: "git", Repo: trustRepo, Owner: "alice"}
	if err := h.st.PutLayerConfig(context.Background(), admin); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=org", map[string]any{"trust_mode": "none"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a non-admin changed an admin-defined layer's policy: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "org"); got.TrustMode != "" {
		t.Errorf("stored mode = %q", got.TrustMode)
	}
	resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{
		"id": "alice-skills", "trust_mode": "require", "trust_requirements": requirement(ownSigner()),
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("user registration: %d %s", resp.StatusCode, body)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=alice-skills", map[string]any{"trust_mode": "warn"}); resp.StatusCode != http.StatusOK {
		t.Errorf("the owner's policy change: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "alice-skills"); !got.UserDefined || got.TrustMode != "warn" {
		t.Errorf("user layer = user_defined %t mode %q", got.UserDefined, got.TrustMode)
	}
}

// Spec: §7.3.1 (Layer trust policy) — a reorder and a restore keep the
// stored policy.
func TestLayerTrust_ReorderAndRestoreKeepPolicy(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	for _, id := range []string{"a", "b"} {
		body := map[string]any{"id": id, "source_type": "local", "local_path": t.TempDir(), "trust_mode": "warn", "trust_layer_root": "skills",
			"trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": "alice@acme.com"})}
		if resp, out := mustPost(t, h.base, "/v1/layers", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %s: %d %s", id, resp.StatusCode, out)
		}
	}
	want := h.stored(t, "a")
	if resp, body := mustPost(t, h.base, "/v1/layers/reorder", map[string]any{"order": []string{"b", "a"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: %d %s", resp.StatusCode, body)
	}
	req, _ := http.NewRequest(http.MethodDelete, h.base+"/v1/layers?id=a", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister: %v %v", resp, err)
	}
	if resp, body := mustPost(t, h.base, "/v1/layers/restore?id=a", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "a"); !layer.TrustPolicyEqual(got, want) || got.Order != 20 {
		t.Errorf("policy after reorder and restore = %q %+v %q order %d", got.TrustMode, got.TrustRequirements, got.TrustLayerRoot, got.Order)
	}
}

// Spec: §7.3.1 (Patch semantics on the update body) — a trust member of the
// wrong JSON type is refused with 400 registry.invalid_argument and writes
// nothing.
func TestLayerTrust_UpdateRefusesMistypedMembers(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	for _, body := range []string{`{"trust_mode":1}`, `{"trust_requirements":"x"}`, `{"trust_layer_root":true}`} {
		resp, out := putRaw(t, h.base, "/v1/layers/update?id=finance", body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(out), "registry.invalid_argument") {
			t.Errorf("%s: %d %s", body, resp.StatusCode, out)
		}
	}
	if got := h.stored(t, "finance"); got.TrustMode != "" || got.TrustRequirements != nil || got.TrustLayerRoot != "" {
		t.Errorf("a refused update stored a policy: %+v", got)
	}
}

// Spec: §7.3.1 (Layer trust policy) — the trust members of a register and an
// update body match without regard to letter case, so update applies what
// register applies; a member carried under two spellings, a misspelled
// trust-prefixed member, and a null trust_mode or trust_layer_root are
// refused with 400 registry.invalid_argument and change nothing. Other
// unknown members are still ignored, so an echoed layer object is admitted.
func TestLayerTrust_MemberNames(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"TRUST_MODE": "warn", "trust_requirements": requirement(ownSigner())})); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "finance"); got.TrustMode != "warn" {
		t.Fatalf("register stored mode %q", got.TrustMode)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"Trust_Mode": "require"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "finance"); got.TrustMode != "require" {
		t.Fatalf("a case-variant member on update stored mode %q", got.TrustMode)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"trust_mode":"warn","Trust_Mode":"none"}`, "more than once"},
		{`{"trust_mod":"none"}`, `"trust_mod" is not a trust policy member`},
		{`{"trust-mode":"none"}`, `"trust-mode" is not a trust policy member`},
		{`{"trust_mode":null}`, "trust_mode is null"},
		{`{"trust_layer_root":null}`, "trust_layer_root is null"},
	} {
		resp, out := putRaw(t, h.base, "/v1/layers/update?id=finance", tc.body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(refusalEnvelope(t, out).Message, tc.want) {
			t.Errorf("update %s: %d %s, want %q", tc.body, resp.StatusCode, out, tc.want)
		}
	}
	resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"id": "other", "trust_mod": "require"}))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(refusalEnvelope(t, out).Message, `"trust_mod" is not a trust policy member`) {
		t.Errorf("register with a misspelled member: %d %s", resp.StatusCode, out)
	}
	if got := h.stored(t, "finance"); got.TrustMode != "require" {
		t.Errorf("a refused update changed the mode to %q", got.TrustMode)
	}
	if resp, out := putRaw(t, h.base, "/v1/layers/update?id=finance", `{"ref":"main","order":5,"created_at":"x"}`); resp.StatusCode != http.StatusOK {
		t.Errorf("an echoed member was refused: %d %s", resp.StatusCode, out)
	}
}

// Spec: §7.3.1 (Layer trust policy) — the policy of a layer declared in
// registry.yaml is set in the declaration: an update or a registration that
// carries a trust member for it is refused, naming registry.yaml, and a
// registration under its ID that carries none keeps the declared policy.
func TestLayerTrust_DeclaredLayerRefusesTrustMembers(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithDeclaredLayers(func(tenant, id string) bool { return tenant == "t" && id == "finance" })
	})
	declared := store.LayerConfig{TenantID: "t", ID: "finance", SourceType: "git", Repo: trustRepo, Ref: "main", TrustMode: "warn",
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: trustIssuer, Identity: "alice@acme.com"}}}}}
	if err := h.st.PutLayerConfig(context.Background(), declared); err != nil {
		t.Fatal(err)
	}
	for _, send := range []func() (*http.Response, []byte){
		func() (*http.Response, []byte) {
			return mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": "require"})
		},
		func() (*http.Response, []byte) {
			return mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "none"}))
		},
	} {
		resp, out := send()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(out), "is declared in registry.yaml") {
			t.Errorf("trust member on a declared layer: %d %s", resp.StatusCode, out)
		}
	}
	if resp, out := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"ref": "release"}); resp.StatusCode != http.StatusOK {
		t.Errorf("an update of another member: %d %s", resp.StatusCode, out)
	}
	if resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-registration without a trust member: %d %s", resp.StatusCode, out)
	}
	if got := h.stored(t, "finance"); !layer.TrustPolicyEqual(got, declared) {
		t.Errorf("declared policy after the re-registration = %q %+v", got.TrustMode, got.TrustRequirements)
	}
}

// Spec: §4.7.11 (Policy rules) — at the endpoints the no-requirement rule
// reads the resolved mode: with PODIUM_LAYER_TRUST_DEFAULT at require, a
// registration with no policy, and an update that empties the mode of a
// layer with no requirement, are refused; a policy with a requirement, and
// an explicit none, are admitted.
func TestLayerTrust_DefaultRequiresAPolicy(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithTrustDefault("require")
	})
	resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(out), "resolves to require through PODIUM_LAYER_TRUST_DEFAULT") {
		t.Fatalf("registration without a policy: %d %s", resp.StatusCode, out)
	}
	if resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "none"})); resp.StatusCode != http.StatusCreated {
		t.Fatalf("explicit none: %d %s", resp.StatusCode, out)
	}
	if resp, out := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": ""}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("emptying the mode with no requirement: %d %s", resp.StatusCode, out)
	}
	if resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"id": "signed", "trust_requirements": requirement(ownSigner())})); resp.StatusCode != http.StatusCreated {
		t.Errorf("a policy with a requirement: %d %s", resp.StatusCode, out)
	}
}

// Spec: §7.3.1 (Layer trust policy) — for a declared layer the endpoints
// refuse only a trust change: an update or a registration that restates the
// stored policy, such as an echoed layer object with its derived members or
// a signer that spells out public-good, is admitted. A registration that
// changes the source while a requirement is declared is refused, and the kept
// policy is checked against the registration's source type.
func TestLayerTrust_DeclaredLayerAdmitsARestatedPolicy(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithDeclaredLayers(func(_, id string) bool { return id == "finance" || id == "local" })
	})
	derived := store.TrustSigner{Issuer: trustIssuer, Identity: trustWorkflow, SourceRepository: trustRepo, SourceRepositoryRef: "refs/heads/main"}
	declared := store.LayerConfig{TenantID: "t", ID: "finance", SourceType: "git", Repo: trustRepo, Ref: "main", TrustMode: "require",
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{derived}}}}
	local := store.LayerConfig{TenantID: "t", ID: "local", SourceType: "local", LocalPath: t.TempDir(), TrustMode: "none", TrustLayerRoot: "skills"}
	for _, cfg := range []store.LayerConfig{declared, local} {
		if err := h.st.PutLayerConfig(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	restated := map[string]any{"trust_mode": "require", "ref": "release",
		"trust_requirements": requirement(map[string]any{"issuer": trustIssuer, "identity": trustWorkflow, "instance": "public-good"})}
	if resp, out := mustPut(t, h.base, "/v1/layers/update?id=finance", restated); resp.StatusCode != http.StatusOK {
		t.Fatalf("a restated policy on update: %d %s", resp.StatusCode, out)
	}
	if got := h.stored(t, "finance"); got.Ref != "release" || !layer.TrustPolicyEqual(got, declared) {
		t.Errorf("after the restating update: ref %q policy %+v", got.Ref, got.TrustRequirements)
	}
	if resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "require", "trust_requirements": requirement(ownSigner())})); resp.StatusCode != http.StatusCreated {
		t.Errorf("a restated policy on registration: %d %s", resp.StatusCode, out)
	}
	if resp, out := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "warn", "trust_requirements": requirement(ownSigner())})); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(refusalEnvelope(t, out).Message, "is declared in registry.yaml") {
		t.Errorf("a changed policy on registration: %d %s", resp.StatusCode, out)
	}
	resp, out := mustPost(t, h.base, "/v1/layers", map[string]any{"id": "finance", "source_type": "local", "local_path": t.TempDir()})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(refusalEnvelope(t, out).Message, "change its source in the declaration") {
		t.Errorf("a source change under a declared requirement: %d %s", resp.StatusCode, out)
	}
	resp, out = mustPost(t, h.base, "/v1/layers", map[string]any{"id": "local", "source_type": "git", "repo": trustRepo, "ref": "main"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(refusalEnvelope(t, out).Message, "trust_layer_root applies to a local layer") {
		t.Errorf("a kept root on a git re-registration: %d %s", resp.StatusCode, out)
	}
	if got := h.stored(t, "local"); got.SourceType != "local" {
		t.Errorf("a refused re-registration wrote source type %q", got.SourceType)
	}
}

// Spec: §4.7.11 (Re-evaluation), §7.3.1 (Layer trust policy) — a register or
// an update that changes the stored trust policy of an existing layer runs
// the re-evaluation hook with the layer as written and the caller; a first
// registration and a write that keeps the policy run none.
func TestLayerTrust_PolicyChangeRunsTheReevaluation(t *testing.T) {
	t.Parallel()
	var calls []string
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithTrustPolicyChange(func(_ context.Context, cfg store.LayerConfig, caller string) {
			calls = append(calls, cfg.ID+" "+cfg.TrustMode+" "+caller)
		})
	})
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(nil)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"ref": "release"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, body)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": "warn", "trust_requirements": requirement(ownSigner())}); resp.StatusCode != http.StatusOK {
		t.Fatalf("policy update: %d %s", resp.StatusCode, body)
	}
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "require", "trust_requirements": requirement(ownSigner())})); resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-registration: %d %s", resp.StatusCode, body)
	}
	if strings.Join(calls, ",") != "finance warn admin,finance require admin" {
		t.Errorf("calls %v", calls)
	}
}

// Spec: §4.7.11 (Layer trust policy, Policy rules), §7.3.1 (Patch semantics
// on the update body) — a registration stores several requirements with
// their covering and optional markers and the listed types; an update
// applies trust_require_types by presence, so null and [] remove the list
// and an update of another trust member keeps it; and the event records the
// previous and the new list.
func TestLayerTrust_SeveralRequirementsAndRequireTypes(t *testing.T) {
	t.Parallel()
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, nil)
	scanner := map[string]any{"issuer": trustIssuer, "identity": "scanner@acme.com"}
	reqs := []any{
		map[string]any{"signers": []any{ownSigner()}, "predicate_type": "https://slsa.dev/provenance/v1", "covers_layer": true},
		map[string]any{"signers": []any{scanner}, "predicate_type": "https://acme.com/scan/v1"},
		map[string]any{"signers": []any{scanner}, "predicate_type": "https://acme.com/review/v1", "optional": true},
	}
	reg := gitTrustBody(map[string]any{"trust_mode": "warn", "trust_requirements": reqs, "trust_require_types": []string{"hook", "mcp-server"}})
	if resp, body := mustPost(t, h.base, "/v1/layers", reg); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	got := h.stored(t, "finance")
	if len(got.TrustRequirements) != 3 || !got.TrustRequirements[0].CoversLayer || got.TrustRequirements[1].Optional ||
		!got.TrustRequirements[2].Optional || strings.Join(got.TrustRequireTypes, ",") != "hook,mcp-server" ||
		got.TrustRequirements[0].Signers[0].SourceRepository != trustRepo {
		t.Fatalf("stored policy = %+v types %v", got.TrustRequirements, got.TrustRequireTypes)
	}
	if ctx := h.lastAuditContext(t); ctx["trust_require_types_before"] != "[]" || ctx["trust_require_types"] != `["hook","mcp-server"]` {
		t.Errorf("register event context = %v", ctx)
	}
	for i, step := range []struct {
		body  map[string]any
		types string
	}{
		{map[string]any{"trust_mode": "require"}, "hook,mcp-server"},
		{map[string]any{"trust_require_types": []string{"agent"}}, "agent"},
		{map[string]any{"trust_require_types": nil}, ""},
		{map[string]any{"trust_require_types": []string{"hook"}}, "hook"},
		{map[string]any{"trust_require_types": []string{}}, ""},
	} {
		if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", step.body); resp.StatusCode != http.StatusOK {
			t.Fatalf("step %d: %d %s", i, resp.StatusCode, body)
		}
		if got := h.stored(t, "finance"); strings.Join(got.TrustRequireTypes, ",") != step.types || len(got.TrustRequirements) != 3 {
			t.Errorf("step %d: types %v, %d requirements, want %q", i, got.TrustRequireTypes, len(got.TrustRequirements), step.types)
		}
	}
	if ctx := h.lastAuditContext(t); ctx["trust_require_types_before"] != `["hook"]` || ctx["trust_require_types"] != "[]" {
		t.Errorf("update event context = %v", ctx)
	}
	// A change that leaves several requirements with no covering marker is
	// refused and writes nothing.
	reqs[0].(map[string]any)["covers_layer"] = false
	resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_requirements": reqs})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "0 of them set covers_layer") {
		t.Fatalf("unmarked update: %d %s", resp.StatusCode, body)
	}
	if got := h.stored(t, "finance"); !got.TrustRequirements[0].CoversLayer {
		t.Errorf("a refused update changed the policy: %+v", got.TrustRequirements)
	}
}
