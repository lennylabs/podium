package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Callers the local-source cases drive. localAlice owns every seeded layer and
// holds no §4.7.2 admin role unless the case installs an admitting arm.
var localAlice = layer.Identity{Sub: "alice", IsAuthenticated: true}

// localTenant is the tenant every local-source case runs in.
const localTenant = "t"

// newLocalSourceEndpoint builds a layer endpoint over a memory store seeded
// with cfgs, whose admin arm admits or denies as admin says and whose identity
// resolver resolves caller. Both callbacks are overridden, because
// NewLayerEndpoint installs an admin arm that admits every caller and a
// resolver that resolves no subject.
func newLocalSourceEndpoint(t *testing.T, admin bool, caller layer.Identity, cfgs ...store.LayerConfig) (*LayerEndpoint, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: localTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, cfg := range cfgs {
		cfg.TenantID = localTenant
		if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
			t.Fatalf("PutLayerConfig(%s): %v", cfg.ID, err)
		}
	}
	e := NewLayerEndpoint(st, localTenant, NewModeTracker()).
		WithAdminAuth(func(*http.Request) error {
			if admin {
				return nil
			}
			return ErrAdminRequired
		}).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return caller, nil })
	return e, st
}

// tombstone soft-deletes a seeded layer so restore has a recoverable target.
func tombstone(t *testing.T, st store.Store, id string) {
	t.Helper()
	if err := st.DeleteLayerConfig(context.Background(), localTenant, id); err != nil {
		t.Fatalf("DeleteLayerConfig(%s): %v", id, err)
	}
}

// localSourceDo drives one request through the layer endpoint's own mux.
func localSourceDo(t *testing.T, e *LayerEndpoint, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = httptest.NewRequest(method, target, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.Handler().ServeHTTP(rec, r)
	return rec
}

// assertLocalSourceRefusal pins the §6.10 envelope the local-source rule
// writes: 403 auth.forbidden carrying details.constraint "local_source",
// retryable false (auth.forbidden has no errorCodeRegistry entry, so it
// reports no suggested action either), and no filesystem path in the body.
func assertLocalSourceRefusal(t *testing.T, rec *httptest.ResponseRecorder, paths ...string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", rec.Body.String(), err)
	}
	if env.Code != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", env.Code)
	}
	if env.Details["constraint"] != "local_source" {
		t.Errorf("details.constraint = %v, want local_source", env.Details["constraint"])
	}
	if env.Retryable {
		t.Errorf("retryable = true, want false")
	}
	for _, p := range append(paths, "/tmp/", "/srv/") {
		if strings.Contains(rec.Body.String(), p) {
			t.Errorf("refusal body discloses the filesystem path %q: %s", p, rec.Body.String())
		}
	}
}

// countingRunner returns a reingest runner that records how often it ran.
func countingRunner(ran *atomic.Int64) ReingestRunner {
	return func(context.Context, store.LayerConfig, *BreakGlass) (*ingest.Result, error) {
		ran.Add(1)
		return &ingest.Result{Accepted: 1}, nil
	}
}

// localLayer is the seeded local-source layer the four HTTP operations run
// against: user-defined and owned by alice, so the layer-write rule admits her
// and the local-source rule is the only refusal left.
func localLayer() store.LayerConfig {
	return store.LayerConfig{
		ID: "own", SourceType: "local", LocalPath: "/tmp/alice-tree",
		UserDefined: true, Owner: "alice", Users: []string{"alice"},
	}
}

// localSourceOp is one of the four HTTP operations the §7.3.1 local-source
// rule guards, driven against a stored local layer alice owns.
type localSourceOp struct {
	name       string
	tombstoned bool
	do         func(t *testing.T, e *LayerEndpoint) *httptest.ResponseRecorder
}

func localSourceOps() []localSourceOp {
	return []localSourceOp{
		{name: "register", do: func(t *testing.T, e *LayerEndpoint) *httptest.ResponseRecorder {
			return localSourceDo(t, e, http.MethodPost, "/v1/layers", map[string]any{
				"id": "fresh", "source_type": "local", "local_path": "/tmp/other-tenant",
			})
		}},
		{name: "update", do: func(t *testing.T, e *LayerEndpoint) *httptest.ResponseRecorder {
			return localSourceDo(t, e, http.MethodPut, "/v1/layers/update?id=own", map[string]any{
				"local_path": "/tmp/other-tenant",
			})
		}},
		{name: "restore", tombstoned: true, do: func(t *testing.T, e *LayerEndpoint) *httptest.ResponseRecorder {
			return localSourceDo(t, e, http.MethodPost, "/v1/layers/restore?id=own", nil)
		}},
		{name: "reingest", do: func(t *testing.T, e *LayerEndpoint) *httptest.ResponseRecorder {
			return localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=own", nil)
		}},
	}
}

// Spec: §7.3.1 — the local-source authorization rule admits a caller the
// §4.7.2 admin arm admits on every operation that names or re-reads a
// filesystem path on the registry host.
func TestLocalSource_AdminArmAdmits(t *testing.T) {
	t.Parallel()
	for _, op := range localSourceOps() {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			e, st := newLocalSourceEndpoint(t, true, localAlice, localLayer())
			if op.tombstoned {
				tombstone(t, st, "own")
			}
			rec := op.do(t, e)
			if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
				t.Fatalf("%s status = %d, want 200 or 201: %s", op.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// Spec: §7.3.1 — a caller the admin arm refuses is refused on every operation
// that names or re-reads a filesystem path on the registry host, with the
// §6.10 envelope the rule names, and the operation writes nothing. alice owns
// the stored layer, so the layer-write rule admits her and this refusal is the
// local-source rule's alone.
func TestLocalSource_DenyingArmRefuses(t *testing.T) {
	t.Parallel()
	for _, op := range localSourceOps() {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			e, st := newLocalSourceEndpoint(t, false, localAlice, localLayer())
			if op.tombstoned {
				tombstone(t, st, "own")
			}
			var ran atomic.Int64
			e = e.WithReingestRunner(countingRunner(&ran))
			rec := op.do(t, e)
			assertLocalSourceRefusal(t, rec)
			if n := ran.Load(); n != 0 {
				t.Errorf("refused %s ran the ingest %d times, want 0", op.name, n)
			}
			ctx := context.Background()
			switch op.name {
			case "register":
				if _, err := st.GetLayerConfig(ctx, localTenant, "fresh"); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("refused register stored the layer: %v", err)
				}
			case "update":
				got, err := st.GetLayerConfig(ctx, localTenant, "own")
				if err != nil {
					t.Fatalf("GetLayerConfig: %v", err)
				}
				if got.LocalPath != "/tmp/alice-tree" {
					t.Errorf("refused update rewrote the stored path: %q", got.LocalPath)
				}
			}
		})
	}
}

// Spec: §7.3.1 — an endpoint that wires no admin arm takes NewLayerEndpoint's
// admitting default, so every local-source operation is admitted there. The
// rule binds only where boot wires an arm that inspects the caller, which is a
// registry with an identity provider configured and not in public mode. A
// reporting surface therefore defaults the other way (D11): an unwired posture
// read reports no capability rather than promising this admission.
func TestLocalSource_UnwiredEndpointAdmits(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: localTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	cfg := localLayer()
	cfg.TenantID = localTenant
	if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	e := NewLayerEndpoint(st, localTenant, NewModeTracker())
	rec := localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=own", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reingest status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := e.Capabilities(httptest.NewRequest(http.MethodGet, "/", nil)); !got.ManageAnyLayer {
		t.Errorf("Capabilities().ManageAnyLayer = false on the admitting default, want true")
	}
}

// Spec: §7.3.1 — a stored layer of a custom §9.1 source type is classified on
// its filesystem path, because the orchestrator hands that path to the
// provider whatever the source type names. The same source type carrying no
// path is admitted, which pins the rule to the path rather than to the type
// being unrecognized.
func TestLocalSource_CustomSourceType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		localPath string
		want      int
	}{
		{name: "with-local-path", localPath: "/tmp/other-tenant", want: http.StatusForbidden},
		{name: "without-local-path", want: http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg := localLayer()
			cfg.SourceType = "acme-vault"
			cfg.LocalPath = c.localPath
			e, _ := newLocalSourceEndpoint(t, false, localAlice, cfg)
			rec := localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=own", nil)
			if rec.Code != c.want {
				t.Fatalf("reingest status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusForbidden {
				assertLocalSourceRefusal(t, rec)
			}
		})
	}
}

// Spec: §7.3.1 — a git layer is classified on its repository string alone, so
// a stored git layer that also carries a filesystem path is reingested and
// restored by its non-admin owner. Git.Snapshot never reads that path, and
// refusing the layer would end its owner's reingest and its webhook deliveries
// permanently while confining nothing. The stored path is named here rather
// than defaulted, so the case fails if the git carve-out is dropped.
func TestLocalSource_StoredGitWithAStrayPath(t *testing.T) {
	t.Parallel()
	ops := []struct {
		name       string
		tombstoned bool
		target     string
	}{
		{name: "reingest", target: "/v1/layers/reingest?id=own"},
		{name: "restore", tombstoned: true, target: "/v1/layers/restore?id=own"},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			cfg := localLayer()
			cfg.SourceType = "git"
			cfg.Repo = "https://github.com/acme/x.git"
			cfg.LocalPath = "/srv/stray"
			e, st := newLocalSourceEndpoint(t, false, localAlice, cfg)
			if op.tombstoned {
				tombstone(t, st, "own")
			}
			rec := localSourceDo(t, e, http.MethodPost, op.target, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200: %s", op.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// Spec: §7.3.1 — update classifies the patch rather than the stored config.
// A patch carrying no local_path is admitted for whatever caller the
// layer-write rule admitted, a patch that names one is refused, and a patch
// that echoes a source_type back is admitted because the handler applies
// neither the source type nor the repository string.
func TestLocalSource_UpdateGuardReadsThePatch(t *testing.T) {
	t.Parallel()

	t.Run("root-only-admitted", func(t *testing.T) {
		t.Parallel()
		e, st := newLocalSourceEndpoint(t, false, localAlice, localLayer())
		rec := localSourceDo(t, e, http.MethodPut, "/v1/layers/update?id=own", map[string]any{"root": "docs"})
		if rec.Code != http.StatusOK {
			t.Fatalf("update status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		got, err := st.GetLayerConfig(context.Background(), localTenant, "own")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		if got.Root != "docs" {
			t.Errorf("stored Root = %q, want docs", got.Root)
		}
		if got.LocalPath != "/tmp/alice-tree" {
			t.Errorf("stored LocalPath = %q, want the seeded path unchanged", got.LocalPath)
		}
	})

	t.Run("local-path-refused", func(t *testing.T) {
		t.Parallel()
		e, _ := newLocalSourceEndpoint(t, false, localAlice, localLayer())
		rec := localSourceDo(t, e, http.MethodPut, "/v1/layers/update?id=own", map[string]any{
			"local_path": "/tmp/other-tenant",
		})
		assertLocalSourceRefusal(t, rec)
	})

	t.Run("echoed-source-type-admitted", func(t *testing.T) {
		t.Parallel()
		cfg := localLayer()
		cfg.SourceType = "git"
		cfg.Repo = "https://github.com/acme/x.git"
		cfg.LocalPath = ""
		e, st := newLocalSourceEndpoint(t, false, localAlice, cfg)
		rec := localSourceDo(t, e, http.MethodPut, "/v1/layers/update?id=own", map[string]any{
			"source_type": "local", "ref": "main",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("update status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		got, err := st.GetLayerConfig(context.Background(), localTenant, "own")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		if got.SourceType != "git" {
			t.Errorf("stored SourceType = %q, want git: the handler applies no source-type patch", got.SourceType)
		}
		if got.Ref != "main" {
			t.Errorf("stored Ref = %q, want main", got.Ref)
		}
	})
}

// repoCase is one repository string and the arm the classifier puts it on.
type repoCase struct {
	name string
	repo string
	file bool
}

func repoCases() []repoCase {
	return []repoCase{
		{name: "https", repo: "https://github.com/acme/x.git"},
		{name: "http", repo: "http://git.acme.com/x"},
		{name: "git-protocol", repo: "git://git.acme.com/x"},
		{name: "ssh-url", repo: "ssh://git@git.acme.com/x.git"},
		{name: "scp-like", repo: "git@github.com:acme/x.git"},
		// go-git reads a "host:path" with no user prefix as ssh, so the
		// classifier admits it too rather than diverging from the transport.
		{name: "host-colon-path", repo: "git.acme.com:acme/x.git"},
		{name: "absolute-path", repo: "/srv/other-tenant", file: true},
		{name: "relative-path", repo: "./x", file: true},
		{name: "file-url", repo: "file:///srv/other-tenant", file: true},
		// The row a hand-written user@host:path predicate gets wrong: go-git
		// rejects the scp reading because the segment before the first ":"
		// carries a "/", and routes the string to the file transport.
		{name: "at-sign-inside-a-path", repo: "/srv/repos@h:x", file: true},
		// A string go-git's parser rejects outright takes the fail-closed arm.
		{name: "unparseable", repo: "http://[::1", file: true},
	}
}

// Spec: §7.3.1 — the repository-string classifier, over the classifier itself
// and over the two operations that read a stored or posted repository string.
// A string go-git resolves to its file transport names a path on the registry
// host and takes the local-source arm; a string it resolves to a network
// transport does not.
func TestLocalSource_RepositoryStrings(t *testing.T) {
	t.Parallel()
	for _, c := range repoCases() {
		t.Run("classifier/"+c.name, func(t *testing.T) {
			t.Parallel()
			if got := isFileTransportRepo(c.repo); got != c.file {
				t.Errorf("isFileTransportRepo(%q) = %v, want %v", c.repo, got, c.file)
			}
		})
		t.Run("register/"+c.name, func(t *testing.T) {
			t.Parallel()
			e, _ := newLocalSourceEndpoint(t, false, localAlice)
			rec := localSourceDo(t, e, http.MethodPost, "/v1/layers", map[string]any{
				"id": "fresh", "source_type": "git", "repo": c.repo,
			})
			if c.file {
				assertLocalSourceRefusal(t, rec, c.repo)
				return
			}
			if rec.Code != http.StatusCreated {
				t.Fatalf("register status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
		})
		t.Run("reingest/"+c.name, func(t *testing.T) {
			t.Parallel()
			cfg := localLayer()
			cfg.SourceType = "git"
			cfg.Repo = c.repo
			cfg.LocalPath = ""
			e, _ := newLocalSourceEndpoint(t, false, localAlice, cfg)
			rec := localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=own", nil)
			if c.file {
				assertLocalSourceRefusal(t, rec, c.repo)
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("reingest status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Spec: §7.3.1 — an empty repository string is classified as nothing, so a
// git registration that names none reaches the ingest path, where
// Git.Snapshot's own ErrInvalidConfig is the validation that answers it.
func TestLocalSource_EmptyRepositoryIsNotClassified(t *testing.T) {
	t.Parallel()
	if isFileTransportRepo("") {
		t.Errorf("isFileTransportRepo(\"\") = true, want false")
	}
	e, _ := newLocalSourceEndpoint(t, false, localAlice)
	rec := localSourceDo(t, e, http.MethodPost, "/v1/layers", map[string]any{
		"id": "fresh", "source_type": "git",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// Spec: §7.3.1 — unregister and reorder name no path and re-read none, so the
// local-source rule does not reach them: a non-admin owner takes them on a
// stored local layer under a denying admin arm, which is the outcome the
// layer-write rule alone gives.
func TestLocalSource_UnregisterAndReorderAreOutsideTheRule(t *testing.T) {
	t.Parallel()
	t.Run("reorder", func(t *testing.T) {
		t.Parallel()
		e, _ := newLocalSourceEndpoint(t, false, localAlice, localLayer())
		rec := localSourceDo(t, e, http.MethodPost, "/v1/layers/reorder", map[string]any{
			"order": []string{"own"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("reorder status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("unregister", func(t *testing.T) {
		t.Parallel()
		e, _ := newLocalSourceEndpoint(t, false, localAlice, localLayer())
		rec := localSourceDo(t, e, http.MethodDelete, "/v1/layers?id=own", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("unregister status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
}

// adminArm is one admin callback and the capability it reports.
type adminArm struct {
	name string
	auth func(*http.Request) error
	want bool
}

func adminArms() []adminArm {
	return []adminArm{
		{name: "admits", auth: func(*http.Request) error { return nil }, want: true},
		{name: "refuses", auth: func(*http.Request) error { return ErrAdminRequired }},
		// A store failure reaches the arm as a wrapped core.ErrUnavailable.
		// The evaluator reports no capability rather than an error, because a
		// prediction that cannot be made withholds.
		{name: "unavailable", auth: func(*http.Request) error {
			return fmt.Errorf("%w: layer store down", core.ErrUnavailable)
		}},
	}
}

// Spec: §7.3.4 — the capability evaluator reports the caller's §7.3.1 layer
// capabilities from the admin arm the endpoint already holds. Any error from
// that arm, an unavailable store included, reports no capability.
func TestLayerEndpoint_Capabilities(t *testing.T) {
	t.Parallel()
	for _, arm := range adminArms() {
		t.Run(arm.name, func(t *testing.T) {
			t.Parallel()
			e, _ := newLocalSourceEndpoint(t, false, localAlice)
			e = e.WithAdminAuth(arm.auth)
			got := e.Capabilities(httptest.NewRequest(http.MethodGet, "/", nil))
			if got.ManageAnyLayer != arm.want {
				t.Errorf("ManageAnyLayer = %v, want %v", got.ManageAnyLayer, arm.want)
			}
		})
	}
}

// Spec: §7.3.4, §7.3.1 — the capability the posture read reports and the gate
// the endpoint applies are one expression. For each admin arm, the value
// Capabilities reports agrees with the outcome of a write on an admin-defined
// layer and with the outcome of a local-source operation, so the two cannot
// drift.
func TestLayerEndpoint_CapabilityMatchesGate(t *testing.T) {
	t.Parallel()
	for _, arm := range adminArms() {
		t.Run(arm.name, func(t *testing.T) {
			t.Parallel()
			adminDefined := store.LayerConfig{ID: "org", SourceType: "git", Repo: "https://github.com/acme/x.git"}
			e, _ := newLocalSourceEndpoint(t, false, localAlice, adminDefined, localLayer())
			e = e.WithAdminAuth(arm.auth)
			caps := e.Capabilities(httptest.NewRequest(http.MethodGet, "/", nil))

			adminWrite := localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=org", nil)
			if got := adminWrite.Code == http.StatusOK; got != caps.ManageAnyLayer {
				t.Errorf("admin-defined reingest admitted = %v, ManageAnyLayer = %v: %s",
					got, caps.ManageAnyLayer, adminWrite.Body.String())
			}
			localWrite := localSourceDo(t, e, http.MethodPost, "/v1/layers/reingest?id=own", nil)
			if got := localWrite.Code == http.StatusOK; got != caps.ManageAnyLayer {
				t.Errorf("local-source reingest admitted = %v, ManageAnyLayer = %v: %s",
					got, caps.ManageAnyLayer, localWrite.Body.String())
			}
		})
	}
}

// webhookDelivery posts a correctly signed GitHub delivery for the layer.
func webhookDelivery(t *testing.T, e *LayerEndpoint, id, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"ref":"refs/heads/main"}`
	sig, err := webhook.Sign("github", []byte(body), secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/"+id, strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	return rec
}

// Spec: §7.3.1 — the inbound webhook ingest takes the local-source arm. A
// delivery carries the per-layer secret rather than a session, so on a
// deployment whose admin arm inspects the caller it resolves no admin: a
// stored git layer whose repository resolves to go-git's file transport is
// refused and runs no ingest, while a layer whose repository names a network
// endpoint reaches the ingest, its stored filesystem path included. The same
// file-transport layer reaches the ingest under the constructor's admitting
// default, which is the deployment that authenticates no caller.
func TestLocalSource_WebhookIngest(t *testing.T) {
	t.Parallel()
	const secret = "hook-secret"
	cases := []struct {
		name      string
		repo      string
		localPath string
		deny      bool
		want      int
	}{
		{name: "file-transport-repo-refused", repo: "/srv/other-tenant", deny: true, want: http.StatusForbidden},
		{name: "file-transport-repo-admitted-unwired", repo: "/srv/other-tenant", want: http.StatusOK},
		{name: "network-repo-admitted", repo: "https://github.com/acme/x.git", deny: true, want: http.StatusOK},
		{name: "network-repo-with-a-stray-path-admitted", repo: "https://github.com/acme/x.git",
			localPath: "/srv/stray", deny: true, want: http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var ran atomic.Int64
			opts := []webhookEndpointOpt{
				func(e *LayerEndpoint) *LayerEndpoint { return e.WithReingestRunner(countingRunner(&ran)) },
			}
			if c.deny {
				opts = append(opts, denyAdminArm)
			}
			e, _ := newWebhookEndpoint(t, store.LayerConfig{
				ID: "vendor", SourceType: "git", Repo: c.repo, LocalPath: c.localPath,
				GitProvider: "github", WebhookSecret: secret,
			}, opts...)
			rec := webhookDelivery(t, e, "vendor", secret)
			if rec.Code != c.want {
				t.Fatalf("delivery status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusForbidden {
				assertLocalSourceRefusal(t, rec, c.repo)
				if n := ran.Load(); n != 0 {
					t.Errorf("refused delivery ran the ingest %d times, want 0", n)
				}
				return
			}
			if n := ran.Load(); n != 1 {
				t.Errorf("admitted delivery ran the ingest %d times, want 1", n)
			}
		})
	}
}
