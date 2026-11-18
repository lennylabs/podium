package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// publicLayerHarness serves the layer and erasure endpoints of a public-mode
// registry whose admin callback is the operator token check, as serverboot
// installs it, over a store holding one layer, "existing".
func publicLayerHarness(t *testing.T, token string) (*server.LayerEndpoint, string, store.Store, *server.ModeTracker) {
	t.Helper()
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "existing", SourceType: "local", LocalPath: "/srv/existing"}); err != nil {
		t.Fatal(err)
	}
	mode := server.NewModeTracker()
	e := server.NewLayerEndpoint(st, "t", mode).WithPublicMode().
		WithAdminAuth(func(r *http.Request) error { return server.CheckOperatorToken(r, token) })
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", e.Handler())
	mux.Handle("/v1/layers/", e.Handler())
	mux.Handle("/v1/admin/erase", e.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return e, ts.URL, st, mode
}

type layerOp struct {
	name, method, path, body string
}

// layerWrites are every operation the operator token rule gates. Each body is
// one the endpoint would act on, and the register body names a host path, so
// a request admitted by mistake would also cross the local-source arm.
var layerWrites = []layerOp{
	{"register", http.MethodPost, "/v1/layers", `{"id":"new","source_type":"local","local_path":"/etc"}`},
	{"update", http.MethodPut, "/v1/layers/update?id=existing", `{"local_path":"/var"}`},
	{"restore", http.MethodPost, "/v1/layers/restore?id=existing", ``},
	{"unregister", http.MethodDelete, "/v1/layers?id=existing", ``},
	{"reorder", http.MethodPost, "/v1/layers/reorder", `{"order":["existing"]}`},
	{"reingest", http.MethodPost, "/v1/layers/reingest?id=existing", ``},
	{"erase", http.MethodPost, "/v1/admin/erase", `{"user_id":"alice@acme.com"}`},
}

// sendOp sends op with the operator token header when token is not empty and
// returns the status, the §6.10 code, and details.constraint.
func sendOp(t *testing.T, base string, op layerOp, token string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(op.method, base+op.path, bytes.NewReader([]byte(op.body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(server.OperatorTokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Code    string `json:"code"`
		Details struct {
			Constraint string `json:"constraint"`
		} `json:"details"`
	}
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Code, env.Details.Constraint
}

func storedLayerIDs(t *testing.T, st store.Store) []string {
	t.Helper()
	all, err := st.ListLayerConfigs(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(all))
	for _, c := range all {
		ids = append(ids, c.ID)
	}
	return ids
}

// Spec: §7.3.1 (Operator token in public mode), §8.5 — on a public-mode
// registry every layer write and an erasure request without the matching
// X-Podium-Operator-Token are refused with 403 auth.forbidden carrying no
// details.constraint, and the stored layers are unchanged.
// Matrix: §6.10 (auth.forbidden)
func TestPublicModeLayerWrites_RefusedWithoutTheToken(t *testing.T) {
	t.Parallel()
	_, base, st, _ := publicLayerHarness(t, testOperatorToken)
	for _, op := range layerWrites {
		for _, token := range []string{"", "wrong-token", testOperatorToken[:31]} {
			status, code, constraint := sendOp(t, base, op, token)
			if status != http.StatusForbidden || code != "auth.forbidden" || constraint != "" {
				t.Errorf("%s with token %q: %d %q constraint %q, want 403 auth.forbidden with no constraint", op.name, token, status, code, constraint)
			}
		}
	}
	if ids := storedLayerIDs(t, st); len(ids) != 1 || ids[0] != "existing" {
		t.Errorf("stored layers after refused writes = %v, want [existing]", ids)
	}
}

// Spec: §7.3.1 (Operator token in public mode) — a public-mode registry with
// no operator token configured refuses every layer write and erasure request,
// including one that sends a token.
func TestPublicModeLayerWrites_RefusedWhileTheTokenIsUnset(t *testing.T) {
	t.Parallel()
	_, base, _, _ := publicLayerHarness(t, "")
	for _, op := range layerWrites {
		if status, code, _ := sendOp(t, base, op, testOperatorToken); status != http.StatusForbidden || code != "auth.forbidden" {
			t.Errorf("%s: %d %q, want 403 auth.forbidden", op.name, status, code)
		}
	}
}

// Spec: §7.3.1 (Operator token in public mode) — the token refusal is
// evaluated before the body and any stored layer are read, so it precedes a
// malformed body, the url_credential refusal, an unknown layer's 404, and the
// local-source refusal, and it follows the read-only refusal (§13.2.1).
func TestPublicModeLayerWrites_RefusalPrecedence(t *testing.T) {
	t.Parallel()
	_, base, _, mode := publicLayerHarness(t, testOperatorToken)
	for _, op := range []layerOp{
		{"malformed body", http.MethodPost, "/v1/layers", `{not json`},
		{"url credential", http.MethodPost, "/v1/layers", `{"id":"g","source_type":"git","repo":"https://bob:secret@git.acme.com/x.git"}`},
		{"unknown layer", http.MethodPut, "/v1/layers/update?id=nope", `{"local_path":"/var"}`},
		{"local source", http.MethodPost, "/v1/layers", `{"id":"h","source_type":"local","local_path":"/etc"}`},
	} {
		if status, code, constraint := sendOp(t, base, op, ""); status != http.StatusForbidden || code != "auth.forbidden" || constraint != "" {
			t.Errorf("%s without the token: %d %q constraint %q, want the bare 403 auth.forbidden", op.name, status, code, constraint)
		}
	}
	mode.Set(server.ModeReadOnly)
	if status, code, _ := sendOp(t, base, layerWrites[0], ""); status != http.StatusServiceUnavailable || code != "registry.read_only" {
		t.Errorf("read-only register without the token: %d %q, want 503 registry.read_only", status, code)
	}
}

// Spec: §7.3.1 (Operator token in public mode), §7.3.4 — a request carrying
// the token is admitted as a tenant admin is, so a host-path registration
// succeeds; the layer list stays whole for a caller without the token; and
// manage_any_layer reports the token.
func TestPublicModeLayerWrites_TokenAdmitsAndListStaysWhole(t *testing.T) {
	t.Parallel()
	e, base, st, _ := publicLayerHarness(t, testOperatorToken)
	if status, code, _ := sendOp(t, base, layerWrites[0], testOperatorToken); status != http.StatusCreated {
		t.Fatalf("register with the token: %d %q, want 201", status, code)
	}
	if status, code, _ := sendOp(t, base, layerWrites[4], testOperatorToken); status == http.StatusForbidden {
		t.Errorf("reorder with the token: 403 %q", code)
	}
	if ids := storedLayerIDs(t, st); len(ids) != 2 {
		t.Errorf("stored layers = %v, want existing and new", ids)
	}

	resp, err := http.Get(base + "/v1/layers")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var listed struct {
		Layers []struct {
			ID string `json:"id"`
		} `json:"layers"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("list body %s: %v", raw, err)
	}
	if len(listed.Layers) != 2 {
		t.Errorf("list without the token = %s, want both layers", raw)
	}

	anon := httptest.NewRequest(http.MethodGet, "/v1/ui/session", nil)
	if e.Capabilities(anon).ManageAnyLayer {
		t.Error("manage_any_layer is true for a request without the token")
	}
	withToken := httptest.NewRequest(http.MethodGet, "/v1/ui/session", nil)
	withToken.Header.Set(server.OperatorTokenHeader, testOperatorToken)
	if !e.Capabilities(withToken).ManageAnyLayer {
		t.Error("manage_any_layer is false for a request with the token")
	}
}

// Spec: §7.3.1 — a registry with no identity provider, not in public mode,
// keeps the open admin arm: a write without a token is admitted.
func TestNoIdentityProviderLayerWrites_NeedNoToken(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	op := layerOp{"register", http.MethodPost, "/v1/layers", `{"id":"new","source_type":"local","local_path":"/etc"}`}
	if status, code, _ := sendOp(t, base, op, ""); status != http.StatusCreated {
		t.Fatalf("register without a token: %d %q, want 201", status, code)
	}
	if ids := storedLayerIDs(t, st); len(ids) != 1 {
		t.Errorf("stored layers = %v, want [new]", ids)
	}
}
