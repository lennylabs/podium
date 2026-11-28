package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// asUserHeader names the identity of each request of the serving fixture.
const asUserHeader = "X-Test-User"

// asPredicateType is the predicate type the serving fixture signs.
const asPredicateType = "https://slsa.dev/provenance/v1"

// servingFixture is a layer under require, visible to alice alone, whose
// signed content one cycle ingested, served over HTTP.
type servingFixture struct {
	st     store.Store
	cfg    *ingest.AttestationConfig
	ts     *httptest.Server
	bundle []byte
}

// newServingFixture ingests a signed two-agent layer into st and serves it.
func newServingFixture(t *testing.T, st store.Store) *servingFixture {
	t.Helper()
	ctx := context.Background()
	h := sh.New(t)
	root, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for p, body := range map[string]string{
		"finance/pay/ARTIFACT.md":   "---\ntype: agent\nversion: 1.0.0\ndescription: Pay an invoice\nsensitivity: low\n---\n\nPay.\n",
		"finance/close/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Close the books\nsensitivity: low\n---\n\nClose.\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content, err := ingest.LayerContent(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: ".podium/layer", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, asPredicateType, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	attDir := filepath.Join(dir, ".podium", "attestations")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, attest.BundleFileName(bundle)), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: dir,
		Users: []string{"alice"}, TrustMode: "require", TrustRequirements: []store.TrustRequirement{{
			PredicateType: asPredicateType,
			Signers:       []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "github"}},
		}}}); err != nil {
		t.Fatal(err)
	}
	cfg := &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github", Root: root}}}
	claim, _, claimed, err := ingest.ClaimLayer(ctx, st, "default", "team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "default", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(dir), Attestation: cfg.Cycle(claimed, claim)}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	reg := core.New(st, "default", nil).WithAttestationPolicyCheck(cfg.PolicyConstraint)
	srv := server.New(reg, server.WithIdentityResolver(func(r *http.Request) layer.Identity {
		sub := r.Header.Get(asUserHeader)
		return layer.Identity{Sub: sub, IsAuthenticated: sub != ""}
	}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &servingFixture{st: st, cfg: cfg, ts: ts, bundle: bundle}
}

// do sends a request as user and returns the status, the headers, and the
// body.
func (f *servingFixture) do(t *testing.T, method, path, user string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(asUserHeader, user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// provenance is the part of a load response or a batch entry the serving
// tests read.
type provenance struct {
	Attestations       []core.AttestationReference `json:"attestations"`
	AttestationOutcome *core.ServedOutcome         `json:"attestation_outcome"`
}

// storesUnderTest returns the in-memory and the SQLite store.
func storesUnderTest(t *testing.T) map[string]func(*testing.T) store.Store {
	return map[string]func(*testing.T) store.Store{
		"memory": func(*testing.T) store.Store { return store.NewMemory() },
		"sqlite": func(t *testing.T) store.Store {
			s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "podium.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
	}
}

// Spec: §7.2 (Attestation members), §7.6.2, §4.7.11 (Attestation storage,
// Attestation outcome) — a load_artifact response and an ok batch entry carry
// the verified link of the served version, with its signer and attested
// time, and the verified outcome.
func TestAttestServing_LoadAndBatchCarryProvenance(t *testing.T) {
	t.Parallel()
	for name, open := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			f := newServingFixture(t, open(t))
			status, _, raw := f.do(t, http.MethodGet, "/v1/load_artifact?id=finance/pay", "alice", nil)
			if status != http.StatusOK {
				t.Fatalf("load = %d: %s", status, raw)
			}
			var load provenance
			if err := json.Unmarshal(raw, &load); err != nil {
				t.Fatal(err)
			}
			digest := attest.AttestationDigest(f.bundle)
			if len(load.Attestations) != 1 {
				t.Fatalf("attestations %+v", load.Attestations)
			}
			a := load.Attestations[0]
			if a.Digest != digest || a.Verdict != "verified" || a.PredicateType != asPredicateType || a.Signer == nil ||
				a.Signer.Instance != "github" || a.Signer.Identity != sh.DefaultSAN || a.Signer.Issuer != sh.DefaultIssuer || a.AttestedAt == "" {
				t.Errorf("attestation %+v signer %+v", a, a.Signer)
			}
			if o := load.AttestationOutcome; o == nil || o.Outcome != "verified" || o.Failure != nil || o.EvaluatedAt == "" {
				t.Errorf("outcome %+v", o)
			}
			status, _, raw = f.do(t, http.MethodPost, "/v1/artifacts:batchLoad", "alice", []byte(`{"ids":["finance/pay"]}`))
			var batch []provenance
			if status != http.StatusOK || json.Unmarshal(raw, &batch) != nil || len(batch) != 1 {
				t.Fatalf("batch = %d: %s", status, raw)
			}
			if len(batch[0].Attestations) != 1 || batch[0].Attestations[0].Digest != digest || batch[0].AttestationOutcome == nil {
				t.Errorf("batch entry %s", raw)
			}
		})
	}
}

// Spec: §7.3.5 (Serving bundles) — GET /v1/attestations/{digest} serves the
// bundle in canonical base64 with the immutable private cache policy to a
// caller who sees a linked version, and answers a caller who sees none, a
// digest the registry does not store, and a malformed digest alike with the
// registry.not_found envelope.
func TestAttestServing_BundleEndpointChecksVisibility(t *testing.T) {
	t.Parallel()
	for name, open := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			f := newServingFixture(t, open(t))
			digest := attest.AttestationDigest(f.bundle)
			status, hdr, raw := f.do(t, http.MethodGet, "/v1/attestations/"+digest, "alice", nil)
			var body struct {
				Bundle string `json:"bundle"`
			}
			if status != http.StatusOK || json.Unmarshal(raw, &body) != nil {
				t.Fatalf("alice = %d: %s", status, raw)
			}
			got, err := base64.StdEncoding.DecodeString(body.Bundle)
			if err != nil || !bytes.Equal(got, f.bundle) || hdr.Get("Cache-Control") != "private, max-age=31536000, immutable" {
				t.Errorf("bundle mismatch (%v), cache control %q", err, hdr.Get("Cache-Control"))
			}
			unknown := "sha256:" + strings.Repeat("0", 64)
			for _, tc := range []struct{ user, digest string }{
				{"bob", digest}, {"", digest}, {"alice", unknown}, {"alice", "sha256:XYZ"},
			} {
				status, hdr, raw := f.do(t, http.MethodGet, "/v1/attestations/"+tc.digest, tc.user, nil)
				var env struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(raw, &env)
				if status != http.StatusNotFound || env.Code != "registry.not_found" || hdr.Get("Cache-Control") != "" {
					t.Errorf("%q %s = %d %s %q", tc.user, tc.digest, status, raw, hdr.Get("Cache-Control"))
				}
			}
		})
	}
}

// Spec: §4.7.11 (Served outcome) — while the layer's policy is invalid, a
// verified version is served as failed with
// ingest.attestation_policy_invalid and the constraint of the rule, and the
// stored outcome is unchanged; a repaired policy serves the stored outcome
// again.
func TestAttestServing_InvalidPolicyServesFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServingFixture(t, store.NewMemory())
	lc, err := f.st.GetLayerConfig(ctx, "default", "team")
	if err != nil {
		t.Fatal(err)
	}
	valid := lc
	lc.TrustRequirements = []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "staging"}}}}
	if err := f.st.PutLayerConfig(ctx, lc); err != nil {
		t.Fatal(err)
	}
	outcome := func() *core.ServedOutcome {
		t.Helper()
		status, _, raw := f.do(t, http.MethodGet, "/v1/load_artifact?id=finance/pay", "alice", nil)
		var load provenance
		if status != http.StatusOK || json.Unmarshal(raw, &load) != nil {
			t.Fatalf("load = %d: %s", status, raw)
		}
		return load.AttestationOutcome
	}
	o := outcome()
	if o == nil || o.Outcome != "failed" || o.Failure == nil || o.Failure.Code != "ingest.attestation_policy_invalid" ||
		o.Failure.Constraint != "unknown_instance" || o.Failure.Scope != "layer" || o.Failure.Requirement != nil {
		t.Fatalf("outcome %+v failure %+v", o, o.Failure)
	}
	if rec, _ := f.st.GetManifest(ctx, "default", "finance/pay", "1.0.0"); rec.AttestationOutcome != store.OutcomeVerified {
		t.Errorf("stored outcome %s", rec.AttestationOutcome)
	}
	if err := f.st.PutLayerConfig(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if o := outcome(); o == nil || o.Outcome != "verified" {
		t.Errorf("outcome after the repair %+v", o)
	}
}
