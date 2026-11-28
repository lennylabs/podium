package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// bvGroupsHeader names the groups of a request of the bootstrap visibility
// fixture.
const bvGroupsHeader = "X-Test-Groups"

// layerReadSwitch fails ListLayerConfigs while fail is set, so a read takes
// the fallback list.
type layerReadSwitch struct {
	store.Store
	fail atomic.Bool
}

func (s *layerReadSwitch) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	if s.fail.Load() {
		return nil, errors.New("layer table unavailable")
	}
	return s.Store.ListLayerConfigs(ctx, tenantID)
}

// bootstrapVisibilityFixture is a layer that --layer-path bootstraps, under
// require, served over HTTP by a registry whose store keeps layer rows.
type bootstrapVisibilityFixture struct {
	t     *testing.T
	h     *sh.Harness
	st    *layerReadSwitch
	cfg   *ingest.AttestationConfig
	dir   string
	ts    *httptest.Server
	signs int
}

func newBootstrapVisibilityFixture(t *testing.T, st store.Store) *bootstrapVisibilityFixture {
	t.Helper()
	ctx := context.Background()
	h := sh.New(t)
	root, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	f := &bootstrapVisibilityFixture{t: t, h: h, st: &layerReadSwitch{Store: st}, dir: t.TempDir(),
		cfg: &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github", Root: root}}}}
	f.write("finance/pay/ARTIFACT.md", "---\ntype: agent\nversion: 1.0.0\ndescription: Pay an invoice\nsensitivity: low\n---\n\nPay.\n")
	f.write(".layer-config", "visibility:\n  groups: [acme-finance]\n")
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: f.dir,
		Groups: []string{"acme-finance"}, VisibilitySource: store.VisibilitySourceDeclared, TrustMode: "require",
		TrustRequirements: []store.TrustRequirement{{PredicateType: asPredicateType,
			Signers: []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "github"}}}}}); err != nil {
		t.Fatal(err)
	}
	reg, err := core.New(f.st, "default", nil).WithLayerRows(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(reg, server.WithIdentityResolver(func(r *http.Request) layer.Identity {
		sub := r.Header.Get(asUserHeader)
		var groups []string
		if g := r.Header.Get(bvGroupsHeader); g != "" {
			groups = strings.Split(g, ",")
		}
		return layer.Identity{Sub: sub, Groups: groups, IsAuthenticated: sub != ""}
	}))
	f.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(f.ts.Close)
	return f
}

func (f *bootstrapVisibilityFixture) write(p, body string) {
	f.t.Helper()
	full := filepath.Join(f.dir, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// sign replaces the attestation directory with one bundle over the layer as
// it stands, each signing later than the last.
func (f *bootstrapVisibilityFixture) sign() {
	f.t.Helper()
	content, err := ingest.LayerContent(os.DirFS(f.dir))
	if err != nil {
		f.t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: ".podium/layer", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	f.signs++
	bundle := f.h.DSSEBundle(sh.Statement(f.t, asPredicateType, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(time.Duration(f.signs)*time.Minute))))
	attDir := filepath.Join(f.dir, ".podium", "attestations")
	if err := os.RemoveAll(attDir); err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(".podium", "attestations", attest.BundleFileName(bundle)), string(bundle))
}

// cycle runs one ingest cycle of the layer and reports whether it passed.
func (f *bootstrapVisibilityFixture) cycle() bool {
	f.t.Helper()
	ctx := context.Background()
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "default", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = ingest.Ingest(ctx, f.st, ingest.Request{TenantID: "default", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(f.dir), Attestation: f.cfg.Cycle(claimed, claim)})
	if err != nil && !errors.Is(err, ingest.ErrAttestationRefused) {
		f.t.Fatalf("cycle: %v", err)
	}
	return err == nil
}

// load returns the status of a load_artifact of finance/pay as user with
// groups.
func (f *bootstrapVisibilityFixture) load(user, groups string) int {
	f.t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/v1/load_artifact?id=finance/pay", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set(asUserHeader, user)
	req.Header.Set(bvGroupsHeader, groups)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// expect checks the status each caller gets: alice in acme-finance, bob
// authenticated in no group, and an anonymous caller.
func (f *bootstrapVisibilityFixture) expect(step string, alice, bob, anonymous int) {
	f.t.Helper()
	if a, b, n := f.load("alice", "acme-finance"), f.load("bob", ""), f.load("", ""); a != alice || b != bob || n != anonymous {
		f.t.Fatalf("%s: alice %d bob %d anonymous %d, want %d %d %d", step, a, b, n, alice, bob, anonymous)
	}
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer), §4.6 (Fallback
// list) — through the HTTP server, under require: an unsigned
// .layer-config that admits more callers changes nothing a caller observes
// and is held pending, a signed one applies once a cycle passes, and an
// unsigned one that admits fewer callers applies in the refused cycle. A
// read that cannot read the layer rows afterwards falls back to the
// narrowed row.
func TestBootstrapVisibility_NarrowingAppliesAndWideningWaits(t *testing.T) {
	t.Parallel()
	for name, open := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			f := newBootstrapVisibilityFixture(t, open(t))
			f.sign()
			if !f.cycle() {
				t.Fatal("the signed cycle was refused")
			}
			f.expect("signed groups", 200, 404, 404)

			f.write(".layer-config", "visibility:\n  public: true\n")
			if f.cycle() {
				t.Fatal("an unsigned widening passed")
			}
			f.expect("unsigned public", 200, 404, 404)
			lc, err := f.st.GetLayerConfig(context.Background(), "default", "team")
			if err != nil || lc.VisibilityPending == nil || !lc.VisibilityPending.Public {
				t.Fatalf("pending after the unsigned widening: %+v %v", lc.VisibilityPending, err)
			}

			f.sign()
			if !f.cycle() {
				t.Fatal("the signed widening was refused")
			}
			f.expect("signed public", 200, 200, 200)

			f.write(".layer-config", "visibility:\n  users: [alice]\n")
			if f.cycle() {
				t.Fatal("an unsigned narrowing passed")
			}
			f.expect("unsigned users", 200, 404, 404)

			f.st.fail.Store(true)
			f.expect("fallback", 200, 404, 404)
		})
	}
}
