package ingest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// fixture builders --------------------------------------------------------

func contextArtifact(desc string) string {
	return "---\n" +
		"type: context\n" +
		"version: 1.0.0\n" +
		"description: " + desc + "\n" +
		"sensitivity: low\n" +
		"---\n\n" +
		"body of " + desc + "\n"
}

func skillArtifact() string {
	return "---\n" +
		"type: skill\n" +
		"version: 1.0.0\n" +
		"sensitivity: low\n" +
		"---\n\n"
}

func skillBody(name string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"description: " + name + "\n" +
		"---\n\n" +
		"prose body of " + name + "\n"
}

func mediumArtifact(desc string) string {
	return "---\n" +
		"type: agent\n" +
		"version: 1.0.0\n" +
		"description: " + desc + "\n" +
		"sensitivity: medium\n" +
		"sbom:\n" +
		"  format: cyclonedx-1.5\n" +
		"  ref: ./sbom.json\n" +
		"---\n\nagent body\n"
}

func newStore(t testing.TB) store.Store {
	t.Helper()
	s := store.NewMemory()
	if err := s.CreateTenant(context.Background(), store.Tenant{ID: "tenant-1", Name: "tenant-1"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return s
}

// tests ------------------------------------------------------------------

// Spec: §7.3.1 Ingest case "New (artifact_id, version)" — accepted;
// content hashed and stored.
func TestIngest_NewArtifactAccepted(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"company-glossary/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("Glossary"))},
	}
	st := newStore(t)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "team-shared",
		Files:    fsys,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 || res.Idempotent != 0 {
		t.Errorf("Accepted=%d, Idempotent=%d", res.Accepted, res.Idempotent)
	}
	got, err := st.GetManifest(context.Background(), "tenant-1", "company-glossary", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !strings.HasPrefix(got.ContentHash, "sha256:") {
		t.Errorf("ContentHash = %q, want sha256: prefix", got.ContentHash)
	}
	if got.Layer != "team-shared" {
		t.Errorf("Layer = %q, want team-shared", got.Layer)
	}
}

// Spec: §7.3.1 Ingest case "Same version, identical content_hash" —
// no-op; handles webhook retries idempotently.
func TestIngest_IdempotentOnSameContent(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"company-glossary/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("Glossary"))},
	}
	st := newStore(t)
	for i := 0; i < 3; i++ {
		res, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "tenant-1",
			LayerID:  "team-shared",
			Files:    fsys,
		})
		if err != nil {
			t.Fatalf("Ingest #%d: %v", i, err)
		}
		switch i {
		case 0:
			if res.Accepted != 1 {
				t.Errorf("first run: Accepted=%d, want 1", res.Accepted)
			}
		default:
			if res.Idempotent != 1 || res.Accepted != 0 {
				t.Errorf("run #%d: Accepted=%d Idempotent=%d", i, res.Accepted, res.Idempotent)
			}
		}
	}
}

// Spec: §7.3.1 / §6.10 — same version with different content_hash
// surfaces as ingest.immutable_violation; existing bytes are preserved.
// Matrix: §6.10 (ingest.immutable_violation)
func TestIngest_ImmutabilityViolation(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	first := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("first"))},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: first,
	}); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	second := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("changed"))},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: second,
	})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(res.Conflicts), res.Conflicts)
	}
	if res.Conflicts[0].ArtifactID != "x" {
		t.Errorf("Conflict.ArtifactID = %q", res.Conflicts[0].ArtifactID)
	}
	// The previously-stored bytes are preserved.
	got, err := st.GetManifest(context.Background(), "tenant-1", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !strings.Contains(string(got.Body), "first") {
		t.Errorf("body should still be first, got %q", got.Body)
	}
}

// Spec: §7.3.1 / §6.10 — lint failures abort per-artifact ingest;
// other artifacts in the same batch still proceed.
// Matrix: §6.10 (ingest.lint_failed)
func TestIngest_LintFailureBlocksThatArtifact(t *testing.T) {
	t.Parallel()
	bad := "---\n" +
		"type: context\n" +
		// Missing version, which is required.
		"description: bad\n" +
		"---\n\nbody\n"
	fsys := fstest.MapFS{
		"good/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("good"))},
		"bad/ARTIFACT.md":  &fstest.MapFile{Data: []byte(bad)},
	}
	st := newStore(t)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: fsys,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted=%d, want 1 (the good one)", res.Accepted)
	}
	if len(res.LintFailures) == 0 {
		t.Errorf("expected lint failures for the bad artifact")
	}
	hasMissingVersion := false
	for _, d := range res.LintFailures {
		if strings.Contains(d.Message, "version") {
			hasMissingVersion = true
		}
	}
	if !hasMissingVersion {
		t.Errorf("expected a lint diagnostic about version: %+v", res.LintFailures)
	}
}

// Spec: §13.10 / §6.10 — public mode rejects ingest of medium and high
// sensitivity artifacts via ingest.public_mode_rejects_sensitive.
// Matrix: §6.10 (ingest.public_mode_rejects_sensitive)
func TestIngest_PublicModeRejectsSensitive(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"agent/ARTIFACT.md": &fstest.MapFile{Data: []byte(mediumArtifact("medium-sense"))},
	}
	st := newStore(t)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:        "tenant-1",
		LayerID:         "public-layer",
		Files:           fsys,
		RejectAtOrAbove: manifest.SensitivityMedium,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 0 {
		t.Errorf("Accepted=%d, want 0", res.Accepted)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1", len(res.Rejected))
	}
	if res.Rejected[0].Code != "ingest.public_mode_rejects_sensitive" {
		t.Errorf("Code = %q", res.Rejected[0].Code)
	}
}

// Spec: §4.4 Bundled Resources — bundled files alongside ARTIFACT.md
// (and SKILL.md) are content-hashed with the manifest so a resource
// change yields a different content hash even when the manifest text
// is identical.
func TestIngest_ContentHashCoversBundledResources(t *testing.T) {
	t.Parallel()

	mkRegistry := func(scriptBody string) fstest.MapFS {
		return fstest.MapFS{
			"finance/run/ARTIFACT.md":    &fstest.MapFile{Data: []byte(skillArtifact())},
			"finance/run/SKILL.md":       &fstest.MapFile{Data: []byte(skillBody("run"))},
			"finance/run/scripts/run.py": &fstest.MapFile{Data: []byte(scriptBody)},
		}
	}

	// First ingest with one script body.
	st1 := newStore(t)
	if _, err := ingest.Ingest(context.Background(), st1, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: mkRegistry("print('a')\n"),
	}); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	rec1, err := st1.GetManifest(context.Background(), "tenant-1", "finance/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest #1: %v", err)
	}

	// Second ingest with a different script body but same manifest.
	// The content hash MUST differ.
	st2 := newStore(t)
	if _, err := ingest.Ingest(context.Background(), st2, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: mkRegistry("print('b')\n"),
	}); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	rec2, err := st2.GetManifest(context.Background(), "tenant-1", "finance/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest #2: %v", err)
	}
	if rec1.ContentHash == rec2.ContentHash {
		t.Errorf("content hashes equal despite different bundled-resource bytes: %s", rec1.ContentHash)
	}
}

// Spec: §4.7.3 Reverse Dependency Index — extends, delegates_to, and
// mcpServers references in the manifest produce dependency edges in
// the store at ingest time.
func TestIngest_PopulatesDependencyEdges(t *testing.T) {
	t.Parallel()
	src := "---\n" +
		"type: agent\n" +
		"version: 1.0.0\n" +
		"description: dependent\n" +
		"sensitivity: low\n" +
		"extends: shared/parent@1.0.0\n" +
		"delegates_to:\n" +
		"  - finance/sub-agent@1.x\n" +
		"mcpServers:\n" +
		"  - name: finance-warehouse\n" +
		"---\n\nbody\n"
	st := newStore(t)
	// Per §4.7.6 the extends: parent must exist at the child's ingest
	// time so its version can be pinned. Ingest the parent first.
	parentSrc := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\nsensitivity: low\n---\n\nparent body\n"
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentSrc)},
		},
	}); err != nil {
		t.Fatalf("Ingest parent: %v", err)
	}
	fsys := fstest.MapFS{
		"finance/dependent/ARTIFACT.md": &fstest.MapFile{Data: []byte(src)},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: fsys,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	cases := []struct {
		to, kind string
	}{
		{"shared/parent", "extends"},
		{"finance/sub-agent", "delegates_to"},
		{"finance-warehouse", "mcpServers"},
	}
	for _, c := range cases {
		got, err := st.DependentsOf(context.Background(), "tenant-1", c.to)
		if err != nil {
			t.Fatalf("DependentsOf(%s): %v", c.to, err)
		}
		found := false
		for _, e := range got {
			if e.From == "finance/dependent" && e.Kind == c.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("expected edge finance/dependent --%s--> %s, got %+v", c.kind, c.to, got)
		}
	}
}

// Spec: §7.3.1 — bytes are preserved when ingest fails on a different
// artifact in the same batch.
func TestIngest_BatchPartialFailure(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	first := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("first-x"))},
		"y/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("first-y"))},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: first,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	second := fstest.MapFS{
		// Conflict on x: same version, different bytes.
		"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("second-x"))},
		// y is unchanged.
		"y/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("first-y"))},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L", Files: second,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].ArtifactID != "x" {
		t.Errorf("expected one conflict on x, got %+v", res.Conflicts)
	}
	if res.Idempotent != 1 {
		t.Errorf("expected y to be idempotent, got Idempotent=%d", res.Idempotent)
	}
	// x is preserved at its original bytes.
	got, err := st.GetManifest(context.Background(), "tenant-1", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest x: %v", err)
	}
	if !strings.Contains(string(got.Body), "first-x") {
		t.Errorf("x body changed: %q", got.Body)
	}
}

// Spec: §7.3.1 — TenantID is required.
func TestIngest_TenantIDRequired(t *testing.T) {
	t.Parallel()
	_, err := ingest.Ingest(context.Background(), newStore(t), ingest.Request{
		LayerID: "L",
		Files:   fstest.MapFS{},
	})
	if err == nil || !strings.Contains(err.Error(), "TenantID") {
		t.Errorf("expected TenantID required error, got %v", err)
	}
}

// Spec: §7.3.1 — Files is required.
func TestIngest_FilesRequired(t *testing.T) {
	t.Parallel()
	_, err := ingest.Ingest(context.Background(), newStore(t), ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "L",
	})
	if err == nil {
		t.Errorf("expected error for missing Files")
	}
}

// Sanity check: ErrLintFailed is exposed for callers to switch on.
func TestIngest_ErrLintFailedExposed(t *testing.T) {
	t.Parallel()
	if !errors.Is(ingest.ErrLintFailed, ingest.ErrLintFailed) {
		t.Errorf("ErrLintFailed should match itself")
	}
}
