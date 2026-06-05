package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

func redactManifest(desc string, redact string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: " + desc +
		"\nsensitivity: low\naudit_redact:\n  - " + redact + "\n---\n\nbody of " + desc + "\n"
}

func setupRegistryWithRedactManifest(t *testing.T, redactKey string) (*core.Registry, *recorder) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(redactManifest("variance", redactKey))},
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithAudit(rec.emit)
	return reg, rec
}

// spec: §8.2 — a manifest's audit_redact directive reaches the
// artifact.loaded read event (not only the ingest publish event), and the
// resolved version/content_hash/layer become the redaction-eligible
// context keys.
func TestAudit_LoadArtifactCarriesRedactKeys(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithRedactManifest(t, "version")

	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/x", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "artifact.loaded" {
		t.Fatalf("got %+v, want one artifact.loaded", events)
	}
	ev := events[0]
	// The directive must be carried so the audit adapter can mask it.
	found := false
	for _, k := range ev.RedactKeys {
		if k == "version" {
			found = true
		}
	}
	if !found {
		t.Errorf("RedactKeys = %v, want it to contain \"version\"", ev.RedactKeys)
	}
	// Eligible read-event context keys mirror the publish event.
	for _, k := range []string{"version", "content_hash", "layer"} {
		if _, ok := ev.Context[k]; !ok {
			t.Errorf("artifact.loaded context missing eligible key %q: %v", k, ev.Context)
		}
	}
	if ev.Context["version"] != "1.0.0" {
		t.Errorf("context version = %q, want the resolved 1.0.0", ev.Context["version"])
	}
}

// spec: §8.2 — a manifest without audit_redact carries no RedactKeys, so
// the read event is written verbatim.
func TestAudit_LoadArtifactNoRedactDirective(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t) // manifest has no audit_redact
	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/x", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	ev := rec.snapshot()[0]
	if len(ev.RedactKeys) != 0 {
		t.Errorf("RedactKeys = %v, want empty for a manifest with no directive", ev.RedactKeys)
	}
}
