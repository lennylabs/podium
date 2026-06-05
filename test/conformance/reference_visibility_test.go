// Spec: §11 Verification — the example artifact registry exercises every
// visibility mode (public, organization, groups, users) and carries signed
// artifacts at multiple sensitivities. These tests load the reference fixture
// through the production server bootstrap and assert that visibility filtering
// and signature verification behave per §4.6 and §4.7.9.
package conformance

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// identityHeader names the request header the test resolver maps to a caller
// identity, so one server can answer as several distinct callers.
const identityHeader = "X-Test-Identity"

// testIdentities maps a header value to the §6.3 identity it stands for. Each
// row models one visibility class the reference fixture declares.
var testIdentities = map[string]layer.Identity{
	// Unauthenticated caller: only public layers are visible.
	"anon": {},
	// Authenticated org member with no group claims: public + organization.
	"org-member": {Sub: "bob", Email: "bob@acme.com", IsAuthenticated: true},
	// Finance group member: public + organization + groups[acme-finance].
	"finance": {Sub: "carol", Email: "carol@acme.com", IsAuthenticated: true, Groups: []string{"acme-finance"}},
	// The personal layer's registrant: public + organization + users[alice].
	"alice": {Sub: "alice", Email: "alice@acme.com", IsAuthenticated: true},
}

// Spec: §11 / §4.6 — a server pointed at the reference fixture via the
// filesystem bootstrap honors each layer's .layer-config visibility, so the
// four visibility modes filter the caller's effective view as declared.
func TestReferenceRegistry_VisibilityFilteringAcrossModes(t *testing.T) {
	t.Parallel()
	resolver := func(r *http.Request) layer.Identity {
		id, ok := testIdentities[r.Header.Get(identityHeader)]
		if !ok {
			return layer.Identity{}
		}
		return id
	}
	srv, err := server.NewFromFilesystem(referencePath(t), server.WithIdentityResolver(resolver))
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Representative artifact IDs, one per layer / visibility mode.
	const (
		orgArtifact      = "company-glossary"                  // org-defaults: public
		sharedArtifact   = "payment-helpers/routing-validator" // _shared: organization
		financeArtifact  = "finance/close/run-variance"        // team-finance: groups
		personalArtifact = "notes/journal"                     // personal: users[alice]
	)

	cases := []struct {
		identity string
		visible  []string
		hidden   []string
	}{
		{"anon", []string{orgArtifact}, []string{sharedArtifact, financeArtifact, personalArtifact}},
		{"org-member", []string{orgArtifact, sharedArtifact}, []string{financeArtifact, personalArtifact}},
		{"finance", []string{orgArtifact, sharedArtifact, financeArtifact}, []string{personalArtifact}},
		{"alice", []string{orgArtifact, sharedArtifact, personalArtifact}, []string{financeArtifact}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.identity, func(t *testing.T) {
			t.Parallel()
			got := searchAllIDs(t, ts.URL, tc.identity)
			for _, want := range tc.visible {
				if !got[want] {
					t.Errorf("identity %q: artifact %q should be visible, got set %v", tc.identity, want, keysOf(got))
				}
			}
			for _, bad := range tc.hidden {
				if got[bad] {
					t.Errorf("identity %q: artifact %q must be hidden, got set %v", tc.identity, bad, keysOf(got))
				}
			}
		})
	}
}

// searchAllIDs runs a browse-mode search (no query) as the named identity and
// returns the set of artifact IDs in the caller's effective view. top_k=50 is
// the §11 cap, large enough to cover the whole fixture.
func searchAllIDs(t *testing.T, baseURL, identity string) map[string]bool {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/search_artifacts?top_k=50", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(identityHeader, identity)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("search_artifacts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search_artifacts status=%d", resp.StatusCode)
	}
	var body struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]bool{}
	for _, r := range body.Results {
		out[r.ID] = true
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Spec: §11 / §4.7.9 — the reference fixture's artifacts sign at ingest and
// verify at materialization time across multiple sensitivities; a tampered
// signature is rejected with materialize.signature_invalid. The Noop provider
// stands in for a real SignatureProvider (Sigstore / registry-managed key);
// the signing and verification control flow is identical.
func TestReferenceRegistry_SignsAndVerifiesAcrossSensitivities(t *testing.T) {
	t.Parallel()
	reg, err := filesystem.Open(referencePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st := store.NewMemory()
	const tenant = "default"
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant, Name: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	provider := sign.Noop{}
	signer := func(ctx context.Context, contentHash string) (string, error) {
		return provider.Sign(ctx, contentHash)
	}
	for _, l := range reg.Layers {
		var layerFS fs.FS = os.DirFS(l.Path)
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: tenant,
			LayerID:  l.ID,
			Files:    layerFS,
			Linter:   lint.NewIngestLinter(true), // offline: skip prose URL probes
			Signer:   signer,
		}); err != nil {
			t.Fatalf("ingest %s: %v", l.ID, err)
		}
	}

	records, err := st.ListManifests(context.Background(), tenant)
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("no manifests ingested from the reference fixture")
	}

	sensitivities := map[string]bool{}
	for _, rec := range records {
		if rec.Signature == "" {
			t.Errorf("artifact %q has no signature; the configured signer must sign every accepted manifest", rec.ArtifactID)
			continue
		}
		// PolicyAlways verifies regardless of sensitivity, exercising the
		// §4.7.9 materialization-time check against the fixture's data.
		if err := sign.EnforceVerification(context.Background(), sign.PolicyAlways, provider, manifest.Sensitivity(rec.Sensitivity), rec.ContentHash, rec.Signature); err != nil {
			t.Errorf("verify %q (sensitivity %q): %v", rec.ArtifactID, rec.Sensitivity, err)
		}
		sensitivities[rec.Sensitivity] = true
	}
	// "Signed artifacts at multiple sensitivities": the fixture spans more
	// than one sensitivity tier and every tier is signed and verifies.
	if len(sensitivities) < 2 {
		t.Errorf("expected signed artifacts at multiple sensitivities, got tiers %v", keysOf(sensitivities))
	}

	// A tampered signature is rejected with materialize.signature_invalid.
	var medium store.ManifestRecord
	for _, rec := range records {
		if rec.Sensitivity == "medium" {
			medium = rec
			break
		}
	}
	if medium.ArtifactID == "" {
		t.Fatalf("fixture carries no medium-sensitivity artifact to tamper")
	}
	err = sign.EnforceVerification(context.Background(), sign.PolicyMediumAndAbove, provider, manifest.Sensitivity(medium.Sensitivity), medium.ContentHash, "noop:tampered")
	if err == nil || !strings.Contains(err.Error(), "signature_invalid") {
		t.Errorf("tampered signature: got err=%v, want signature_invalid", err)
	}
}
