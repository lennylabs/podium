package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// rcArtifact returns an artifact's source, with an extends: line when
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
	ArtifactID string         `json:"artifact_id"`
	Code       string         `json:"code"`
	Reason     string         `json:"reason"`
	Details    map[string]any `json:"details"`
}

// rcReingestStatus seeds the hidden admin layer with shared/note, ingests it,
// gives alice a user-defined layer holding aliceTree, reingests that layer as
// alice over POST /v1/layers/reingest, and returns the store, the status, and
// the raw body, whatever the status is.
func rcReingestStatus(t *testing.T, aliceTree map[string]string) (store.Store, int, []byte) {
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
	core := store.LayerConfig{
		TenantID: "t", ID: rcHiddenLayer, Order: 1, SourceType: "local", LocalPath: coreDir,
		Users: []string{"ops@acme.com"},
	}
	if err := st.PutLayerConfig(ctx, core); err != nil {
		t.Fatalf("PutLayerConfig(core): %v", err)
	}
	res, err := localReingestRunner(st, nil)(ctx, core, nil)
	if err != nil || len(res.Rejected) > 0 || res.Accepted != 1 {
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
	return st, status, body
}

// rcReingest is rcReingestStatus for a cycle answered with 200. It fails the
// test on any other status and returns the raw body and the rejections keyed
// by artifact ID.
func rcReingest(t *testing.T, aliceTree map[string]string) ([]byte, map[string]rcReject) {
	t.Helper()
	_, status, body := rcReingestStatus(t, aliceTree)
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

// Spec: §4.6, §7.3.1 — the cross-layer collision rejection a non-admin
// layer owner receives from POST /v1/layers/reingest names the colliding
// artifact ID, tells the author to rename it, and carries the ingest.collision
// code. It names no layer, because the layer that already contributes the ID
// is one alice cannot read, and it no longer offers extends: as a remedy.
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
	for _, want := range []string{`"shared/note"`, "rename"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q missing %q", got.Reason, want)
		}
	}
	if strings.Contains(got.Reason, "extends") {
		t.Errorf("reason %q offers extends:", got.Reason)
	}
	if strings.Contains(string(body), rcHiddenLayer) {
		t.Errorf("reingest response names the hidden layer %q: %s", rcHiddenLayer, body)
	}
}

// Spec: §4.6, §7.3.1 — a reingest that refuses an ARTIFACT.md declaring
// extends: answers 200 and reports the refusal with ingest.invalid_artifact,
// the §4.6 no-inheritance reason, and a details member holding exactly the
// extends_removed constraint. A rejection whose rule defines no details, the
// collision beside it, carries no details key, and the body names no hidden
// layer.
func TestReingest_ExtendsRemovedDetails(t *testing.T) {
	t.Parallel()
	body, rejected := rcReingest(t, map[string]string{
		"mine/child":  rcArtifact("context", "1.0.0", "shared/note", ""),
		"shared/note": rcArtifact("context", "2.0.0", "", ""),
	})
	got, ok := rejected["mine/child"]
	if !ok {
		t.Fatalf("mine/child not rejected: %s", body)
	}
	if got.Code != "ingest.invalid_artifact" || got.Reason != layer.ExtendsRemovedReason {
		t.Errorf("rejection = %+v, want ingest.invalid_artifact with the extends_removed reason", got)
	}
	if strings.Contains(string(body), rcHiddenLayer) {
		t.Errorf("reingest response names the hidden layer %q: %s", rcHiddenLayer, body)
	}

	var raw struct {
		Rejected []map[string]json.RawMessage `json:"rejected"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode reingest response %s: %v", body, err)
	}
	seen := map[string]bool{}
	for _, entry := range raw.Rejected {
		d, has := entry["details"]
		switch id := string(entry["artifact_id"]); id {
		case `"mine/child"`:
			seen[id] = true
			var compact bytes.Buffer
			if err := json.Compact(&compact, d); err != nil || compact.String() != `{"constraint":"extends_removed"}` {
				t.Errorf("mine/child details = %s (%v), want exactly {\"constraint\":\"extends_removed\"}", d, err)
			}
		case `"shared/note"`:
			seen[id] = true
			if has {
				t.Errorf("shared/note collision entry carries details %s, want no details key", d)
			}
		}
	}
	if len(seen) != 2 {
		t.Errorf("rejected entries seen = %v, want mine/child and shared/note: %s", seen, body)
	}
}

// Spec: §4.6, §7.3.1 — the rejected list and its details belong to a cycle
// answered without a §6.10 error envelope. A cycle that accepts nothing and
// holds a lint failure is answered with the ingest.lint_failed 422 envelope,
// which reports neither the extends_removed refusal beside the lint failure
// nor its details, and the refused artifact is not stored. The omission is the
// existing envelope behavior, so a later change that carries rejected into the
// envelope shows here as a test change.
func TestReingest_ExtendsRemovedUnderLintEnvelope(t *testing.T) {
	t.Parallel()
	// The hook omits hook_action, which the ingest linter refuses.
	hook := "---\ntype: hook\nversion: 1.0.0\ndescription: fixture hook\nsensitivity: low\n" +
		"hook_event: stop\n---\n\nbody\n"
	st, status, body := rcReingestStatus(t, map[string]string{
		"mine/child": rcArtifact("context", "1.0.0", "shared/note", ""),
		"mine/hook":  hook,
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("reingest status = %d, want 422: %s", status, body)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	if string(env["code"]) != `"ingest.lint_failed"` {
		t.Errorf("code = %s, want ingest.lint_failed: %s", env["code"], body)
	}
	if _, has := env["rejected"]; has {
		t.Errorf("422 envelope carries a rejected member: %s", body)
	}
	if strings.Contains(string(body), "extends_removed") {
		t.Errorf("422 envelope reports the extends_removed refusal: %s", body)
	}
	if _, err := st.GetManifest(context.Background(), "t", "mine/child", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetManifest(mine/child) err = %v, want store.ErrNotFound", err)
	}
}
