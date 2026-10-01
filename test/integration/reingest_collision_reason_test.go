package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// rcHiddenLayer is the admin-defined layer alice cannot read. Its ID is
// distinctive so a substring search of the reingest response finds any leak.
const rcHiddenLayer = "core-private-xq7"

// rcWrite writes one ARTIFACT.md under dir/id.
func rcWrite(t *testing.T, dir, id, src string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(id))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(p, "ARTIFACT.md"), []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", id, err)
	}
}

// rcArtifact returns a context artifact's source, with an extends: line when
// extends is non-empty and the extra frontmatter lines appended.
func rcArtifact(typ, ver, extends, extra string) string {
	src := "---\ntype: " + typ + "\nversion: " + ver + "\ndescription: fixture artifact\nsensitivity: low\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	return src + extra + "---\n\nbody\n"
}

// rcReject is one entry of the reingest response's rejected list.
type rcReject struct {
	ArtifactID string `json:"artifact_id"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
}

// rcReingest seeds the hidden admin layer with parents, ingests it, gives
// alice a user-defined layer holding aliceTree, reingests that layer as alice
// over POST /v1/layers/reingest, and returns the raw body and the rejections
// keyed by artifact ID.
func rcReingest(t *testing.T, aliceTree map[string]string) ([]byte, map[string]rcReject) {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	coreDir := t.TempDir()
	rcWrite(t, coreDir, "shared/note", rcArtifact("context", "1.0.0", "", ""))
	rcWrite(t, coreDir, "core/agentparent", rcArtifact("agent", "1.0.0", "", ""))
	rcWrite(t, coreDir, "core/ctxparent", rcArtifact("context", "1.0.0", "", ""))
	rcWrite(t, coreDir, "core/old", rcArtifact("context", "1.0.0", "", "deprecated: true\n"))
	core := store.LayerConfig{
		TenantID: "t", ID: rcHiddenLayer, Order: 1, SourceType: "local", LocalPath: coreDir,
		Users: []string{"ops@acme.com"},
	}
	if err := st.PutLayerConfig(ctx, core); err != nil {
		t.Fatalf("PutLayerConfig(core): %v", err)
	}
	res, err := localReingestRunner(st, nil)(ctx, core, nil)
	if err != nil || len(res.Rejected) > 0 || res.Accepted != 4 {
		t.Fatalf("ingest core: err=%v accepted=%d rejected=%+v", err, res.Accepted, res.Rejected)
	}

	// alice-personal names a git source so the §7.3.1 local-source rule does
	// not refuse its non-admin owner; the runner reads LocalPath regardless.
	userDir := t.TempDir()
	for id, src := range aliceTree {
		rcWrite(t, userDir, id, src)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", Order: 2, SourceType: "git",
		Repo: "https://github.com/alice/personal.git", LocalPath: userDir,
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
	}); err != nil {
		t.Fatalf("PutLayerConfig(alice-personal): %v", err)
	}

	alice := layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}
	base := newLayerWriteEndpoint(t, st, alice)
	status, body := layerWritePost(t, base, "/v1/layers/reingest?id=alice-personal", nil)
	if status != http.StatusOK {
		t.Fatalf("reingest status = %d, want 200: %s", status, body)
	}
	var out struct {
		Rejected []rcReject `json:"rejected"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode reingest response %s: %v", body, err)
	}
	byID := map[string]rcReject{}
	for _, r := range out.Rejected {
		byID[r.ArtifactID] = r
	}
	return body, byID
}

// Spec: §4.6 hidden parents (observable) — the cross-layer collision rejection
// a non-admin layer owner receives from POST /v1/layers/reingest names the
// colliding artifact ID, the `declare extends:` remedy, and the
// ingest.collision code, and names no layer, because the layer that already
// contributes the ID is one alice cannot read.
func TestReingest_CollisionReasonNamesNoLayer(t *testing.T) {
	t.Parallel()
	body, rejected := rcReingest(t, map[string]string{
		"shared/note": rcArtifact("context", "2.0.0", "", ""),
	})
	got, ok := rejected["shared/note"]
	if !ok {
		t.Fatalf("shared/note not rejected: %s", body)
	}
	if got.Code != "ingest.collision" {
		t.Errorf("code = %q, want ingest.collision", got.Code)
	}
	for _, want := range []string{`"shared/note"`, "declare extends: shared/note"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q missing %q", got.Reason, want)
		}
	}
	if strings.Contains(string(body), rcHiddenLayer) {
		t.Errorf("reingest response names the hidden layer %q: %s", rcHiddenLayer, body)
	}
}

// Spec: §4.6 hidden parents (observable) — resolving an extends: reference
// discloses its resolution to the author who wrote it, so the reingest
// rejections other than the collision keep naming the parent and why it did
// not resolve: that it does not exist, that its type differs, that no stored
// version satisfies the constraint, and that its versions are deprecated.
// None of them names the layer that contributes the parent.
func TestReingest_ExtendsResolutionReasonsSurvive(t *testing.T) {
	t.Parallel()
	body, rejected := rcReingest(t, map[string]string{
		"mine/missing": rcArtifact("context", "1.0.0", "core/absent", ""),
		"mine/typed":   rcArtifact("context", "1.0.0", "core/agentparent", ""),
		"mine/range":   rcArtifact("context", "1.0.0", "core/ctxparent@9.x", ""),
		"mine/dep":     rcArtifact("context", "1.0.0", "core/old", ""),
	})
	cases := map[string][]string{
		"mine/missing": {`no parent artifact "core/absent"`},
		"mine/typed":   {"core/agentparent", `type "agent"`},
		"mine/range":   {"no parent version satisfies", "core/ctxparent@9.x"},
		"mine/dep":     {`parent "core/old"`, "deprecated"},
	}
	for id, wants := range cases {
		got, ok := rejected[id]
		if !ok {
			t.Errorf("%s not rejected: %s", id, body)
			continue
		}
		if got.Code != "ingest.invalid_artifact" {
			t.Errorf("%s code = %q, want ingest.invalid_artifact", id, got.Code)
		}
		for _, want := range wants {
			if !strings.Contains(got.Reason, want) {
				t.Errorf("%s reason %q missing %q", id, got.Reason, want)
			}
		}
	}
	if strings.Contains(string(body), rcHiddenLayer) {
		t.Errorf("reingest response names the hidden layer %q: %s", rcHiddenLayer, body)
	}
}
