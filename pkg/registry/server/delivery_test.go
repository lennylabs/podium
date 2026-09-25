package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
)

// deliveryTree writes a one-layer registry holding a parent, a child that
// extends it, and a plain artifact.
func deliveryTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{Path: "shared/parent/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nparent\n"},
		testharness.WriteTreeOption{Path: "team/child/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: child\nextends: shared/parent@1.0.0\n---\n\nchild\n"},
		testharness.WriteTreeOption{Path: "team/plain/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: plain\n---\n\nplain\n"},
	)
	return dir
}

// deliveryServer serves deliveryTree with signer as the delivery signer.
func deliveryServer(t *testing.T, signer sign.Provider) *httptest.Server {
	t.Helper()
	srv, err := server.NewFromFilesystem(deliveryTree(t), server.WithDeliverySigner(signer))
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// envelopeKeys returns the sorted keys of a registry-managed envelope.
func envelopeKeys(t *testing.T, envelope string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(envelope), &m); err != nil {
		t.Fatalf("envelope %q is not JSON: %v", envelope, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// Spec: §4.7.10 — a non-extends artifact carries a delivery_hash and a
// delivery_signature of the same form as a merged child's, so the presence
// and form of the attestation disclose nothing about a merge.
func TestLoadArtifact_UniformAttestationAcrossMergedAndPlain(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ts := deliveryServer(t, sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub})
	var forms []string
	for _, id := range []string{"team/child", "team/plain"} {
		status, body := getBody(t, ts.URL+"/v1/load_artifact?id="+id)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d: %s", id, status, body)
		}
		var resp server.LoadArtifactResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.HasPrefix(resp.DeliveryHash, "sha256:") || len(resp.DeliveryHash) != len("sha256:")+64 {
			t.Errorf("%s delivery_hash = %q, want sha256:<64 hex>", id, resp.DeliveryHash)
		}
		if err := (sign.RegistryManagedKey{PublicKey: pub}).Verify(context.Background(), resp.DeliveryHash, resp.DeliverySignature); err != nil {
			t.Errorf("%s delivery signature does not verify: %v", id, err)
		}
		forms = append(forms, envelopeKeys(t, resp.DeliverySignature))
	}
	if forms[0] != forms[1] {
		t.Errorf("envelope forms differ: merged %s, plain %s", forms[0], forms[1])
	}
}

// failingSigner is a sign.Provider whose Sign always fails.
type failingSigner struct{}

func (failingSigner) ID() string { return "failing" }
func (failingSigner) Sign(context.Context, string) (string, error) {
	return "", errors.New("signer offline")
}
func (failingSigner) Verify(context.Context, string, string) error { return nil }

// Spec: §4.7.10 — a registry that cannot sign a delivery hash serves no
// unsigned record in its place: the single load answers registry.unavailable
// and the batch entry carries an error envelope.
func TestLoadArtifact_DeliverySignerFailureRefusesTheLoad(t *testing.T) {
	t.Parallel()
	ts := deliveryServer(t, failingSigner{})
	status, body := getBody(t, ts.URL+"/v1/load_artifact?id=team/plain")
	if status != http.StatusInternalServerError || !strings.Contains(string(body), "registry.unavailable") {
		t.Errorf("single load: status %d body %s, want 500 registry.unavailable", status, body)
	}
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(`{"ids":["team/plain"]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var envs []server.BatchLoadEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envs); err != nil || len(envs) != 1 {
		t.Fatalf("decode batch: %v %v", err, envs)
	}
	if envs[0].Status != "error" || envs[0].Error == nil || envs[0].DeliveryHash != "" {
		t.Errorf("batch entry = %+v, want an error envelope and no attestation", envs[0])
	}
}

// Spec: §4.7.10 — a registry with no delivery signer serves delivery_hash
// with no delivery_signature key.
func TestLoadArtifact_NoDeliverySignerServesTheHashAlone(t *testing.T) {
	t.Parallel()
	ts := deliveryServer(t, nil)
	status, body := getBody(t, ts.URL+"/v1/load_artifact?id=team/plain")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(string(body), `"delivery_hash"`) || strings.Contains(string(body), `"delivery_signature"`) {
		t.Errorf("body %s, want delivery_hash and no delivery_signature", body)
	}
}
