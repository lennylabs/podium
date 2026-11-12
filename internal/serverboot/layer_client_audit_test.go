package serverboot

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// clientLayerID is the local layer the caller.client layer tests register.
const clientLayerID = "client-layer"

// sendWithClient drives one layer-route request against the booted registry
// with the X-Podium-Client header and returns the status code and body.
func sendWithClient(t *testing.T, addr, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(audit.ClientHeader, testClientHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// registerClientLayer registers a local layer over root through the
// TenantRouted /v1/layers mount and requires 201.
func registerClientLayer(t *testing.T, addr, root string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"id": clientLayerID, "source_type": "local", "local_path": root,
	})
	if err != nil {
		t.Fatalf("marshal register body: %v", err)
	}
	if status, resp := sendWithClient(t, addr, http.MethodPost, "/v1/layers", string(body)); status != http.StatusCreated {
		t.Fatalf("register status = %d, want 201: %s", status, resp)
	}
}

// reingestClientLayer runs POST /v1/layers/reingest on the registered layer
// and requires 200.
func reingestClientLayer(t *testing.T, addr string) {
	t.Helper()
	if status, resp := sendWithClient(t, addr, http.MethodPost, "/v1/layers/reingest?id="+clientLayerID, ""); status != http.StatusOK {
		t.Fatalf("reingest status = %d, want 200: %s", status, resp)
	}
}

// Spec: §7.2.2, §8.1 — the layer routes sit outside the server's audit
// metadata middleware, so a layer write reaches caller.client through the
// endpoint's own metadata fallback. A registration sent with the header
// through the TenantRouted /v1/layers mount records it on
// layer.config_changed.
func TestRun_LayerRegisterRecordsCallerClient(t *testing.T) {
	f := newBootFixture(t)
	root := filepath.Join(f.home, "client-layer")
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "beta/ARTIFACT.md", Content: artifactBody})
	if _, err := f.bootWith(t, func() { registerClientLayer(t, f.addr, root) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, ev := range readAuditLines(t, f.auditPath) {
		if ev.Type == string(audit.EventLayerConfigChanged) && ev.Target == clientLayerID {
			if !reflect.DeepEqual(ev.Caller.Client, testClient) {
				t.Errorf("layer.config_changed caller.client = %v, want %v", ev.Caller.Client, testClient)
			}
			return
		}
	}
	t.Errorf("no layer.config_changed record for %s in the audit log", clientLayerID)
}

// ingestEventTypes are the events the ingest pipeline raises that the
// reingest test provokes.
var ingestEventTypes = []audit.EventType{
	audit.EventArtifactPublished,
	audit.EventArtifactDeprecated,
	audit.EventArtifactSigned,
	audit.EventDomainPublished,
	audit.EventLayerIngested,
}

// Spec: §7.2.2, §8.1 — every event the ingest pipeline raises records no
// caller.client, even when a reingest request that names its client triggered
// it. The layer holds a DOMAIN.md and one artifact under a registry signer, so
// the first reingest publishes, signs, and records the domain, and the second
// publishes a new version that sets deprecated: true. Each event type must
// appear, so an empty stream cannot pass.
func TestRun_ReingestEventsRecordNoCallerClient(t *testing.T) {
	f := newSignPassFixture(t, testSigner(t))
	root := filepath.Join(f.home, "client-layer")
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "finance/DOMAIN.md", Content: "---\ndescription: finance\n---\n"},
		testharness.WriteTreeOption{Path: "finance/close/ARTIFACT.md", Content: artifactBody},
	)
	deprecated := strings.Replace(artifactBody, "version: 1.0.0\n", "version: 1.1.0\ndeprecated: true\n", 1)
	if _, err := f.bootWith(t, func() {
		registerClientLayer(t, f.addr, root)
		reingestClientLayer(t, f.addr)
		testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "finance/close/ARTIFACT.md", Content: deprecated})
		reingestClientLayer(t, f.addr)
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	seen := map[string]int{}
	for _, ev := range readAuditLines(t, f.auditPath) {
		if !slices.Contains(ingestEventTypes, audit.EventType(ev.Type)) {
			continue
		}
		seen[ev.Type]++
		if ev.Caller.Client != nil {
			t.Errorf("%s %s caller.client = %v, want none", ev.Type, ev.Target, ev.Caller.Client)
		}
	}
	for _, typ := range ingestEventTypes {
		if seen[string(typ)] == 0 {
			t.Errorf("no %s record in the audit log; seen %v", typ, seen)
		}
	}
}
