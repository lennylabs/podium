package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// escapingLayerTree builds a local layer directory holding one DOMAIN.md, one
// artifact, and one bundled resource symlinked to a file outside the root, and
// returns the parent and the layer root.
func escapingLayerTree(t *testing.T) (parent, root string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "layer")
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "finance/DOMAIN.md", Content: "---\nname: Finance\n---\n\nFinance domain.\n"},
		testharness.WriteTreeOption{Path: "finance/pay-invoice/ARTIFACT.md", Content: agentArtifact("Pay an invoice")},
	)
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("outside secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "..", "outside.txt"), filepath.Join(root, "finance", "pay-invoice", "leak.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	return parent, root
}

// Spec: §7.3.1, §6.10 — a bundled resource reached through a symbolic link
// that leaves the layer directory fails the layer's whole ingest with the
// source-unreachable sentinel. Spec: §4.7.11 (Ingest verification) — both
// walks run before the cycle's first store write, so the refused cycle
// writes neither an artifact nor a domain record and emits no
// domain.published.
func TestSourceIngest_EscapingResourceFailsTheLayer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parent, root := escapingLayerTree(t)
	st := newStore(t)

	var published []string
	var audited []domainAuditEvent
	req := func() ingest.Request {
		return ingest.Request{
			TenantID: "tenant-1",
			LayerID:  "team-shared",
			Files:    source.ConfinedFS(root),
			PublishEvent: func(_ context.Context, _ core.EventScope, eventType string, _ map[string]any) {
				published = append(published, eventType)
			},
			AuditEmit: collectDomainAudit(&audited),
		}
	}

	_, err := ingest.Ingest(ctx, st, req())
	if err == nil {
		t.Fatalf("Ingest accepted a layer holding an escaping symbolic link")
	}
	if !errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("error %q is not source.ErrSourceUnreachable", err)
	}
	if !strings.Contains(err.Error(), "finance/pay-invoice/leak.txt") {
		t.Errorf("error does not name the refused path relative to the root: %v", err)
	}
	if strings.Contains(err.Error(), parent) || strings.Contains(err.Error(), root) {
		t.Errorf("error leaks a host path: %v", err)
	}
	manifests, err := st.ListManifests(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("refused ingest persisted %d artifacts, want 0", len(manifests))
	}
	domains, err := st.ListDomains(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 0 || len(published) != 0 || countDomainPublished(audited) != 0 {
		t.Fatalf("refused cycle wrote %d domain records and emitted %d change events and %d audited domain.published, want none",
			len(domains), len(published), countDomainPublished(audited))
	}
	firstPublished := len(published)
	firstAudited := countDomainPublished(audited)

	// A second refused cycle over the same unchanged tree writes nothing
	// either.
	if _, err := ingest.Ingest(ctx, st, req()); !errors.Is(err, source.ErrSourceUnreachable) {
		t.Fatalf("second cycle: want ErrSourceUnreachable, got %v", err)
	}
	domains, err = st.ListDomains(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("second cycle: want no domain record, got %d", len(domains))
	}
	if len(published) != firstPublished {
		t.Errorf("second cycle emitted %d further change events, want 0", len(published)-firstPublished)
	}
	if got := countDomainPublished(audited); got != firstAudited {
		t.Errorf("second cycle emitted %d further audited domain.published, want 0", got-firstAudited)
	}

	// With the escaping link removed the same tree ingests cleanly, so the
	// confinement refuses the escape rather than the layer.
	if err := os.Remove(filepath.Join(root, "finance", "pay-invoice", "leak.txt")); err != nil {
		t.Fatalf("Remove link: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, req()); err != nil {
		t.Fatalf("clean cycle: %v", err)
	}
	if _, err := st.GetManifest(ctx, "tenant-1", "finance/pay-invoice", "1.0.0"); err != nil {
		t.Errorf("clean cycle did not land the artifact: %v", err)
	}
}

// Spec: §7.3.1, §6.10 — a SKILL.md reached only through an escaping symbolic
// link keeps the source-unreachable sentinel. Spec: §4.1 — an absent SKILL.md
// is refused per artifact with a reason that names the missing file.
func TestSourceIngest_EscapingSkillFileClassifies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	parent := t.TempDir()
	root := filepath.Join(parent, "layer")
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ops/runbook/ARTIFACT.md", Content: skillArtifact()},
	)
	if err := os.WriteFile(filepath.Join(parent, "SKILL.md"), []byte(skillBody("runbook")), 0o644); err != nil {
		t.Fatalf("WriteFile outside SKILL.md: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "..", "SKILL.md"), filepath.Join(root, "ops", "runbook", "SKILL.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	_, err := ingest.Ingest(ctx, newStore(t), ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "team-shared",
		Files:    source.ConfinedFS(root),
	})
	if err == nil {
		t.Fatalf("Ingest accepted a skill whose SKILL.md leaves the layer directory")
	}
	if !errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("error %q is not source.ErrSourceUnreachable", err)
	}

	absentRoot := filepath.Join(t.TempDir(), "layer")
	testharness.WriteTree(t, absentRoot,
		testharness.WriteTreeOption{Path: "ops/runbook/ARTIFACT.md", Content: skillArtifact()},
	)
	res, err := ingest.Ingest(ctx, newStore(t), ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "team-shared",
		Files:    source.ConfinedFS(absentRoot),
	})
	// Spec: §4.1 — an absent SKILL.md is a per-artifact refusal under the
	// unparseable-manifest rule, so the cycle succeeds and names it.
	if err != nil {
		t.Fatalf("Ingest with an absent SKILL.md = %v, want a per-artifact refusal", err)
	}
	if res.Accepted != 0 || len(res.Rejected) != 1 {
		t.Fatalf("accepted %d, rejected %+v, want one refusal", res.Accepted, res.Rejected)
	}
	rej := res.Rejected[0]
	if rej.ArtifactID != "ops/runbook" || rej.Details["constraint"] != layer.ManifestUnparseableConstraint {
		t.Errorf("refusal = %+v, want ops/runbook with manifest_unparseable", rej)
	}
	if !strings.Contains(rej.Reason, "ops/runbook/SKILL.md: manifest: type: skill missing SKILL.md") {
		t.Errorf("refusal reason %q does not name the missing SKILL.md", rej.Reason)
	}
}

// Spec: §7.3.1, §6.10, §4.2 — package discovery stats each manifest through
// the confined source, so a manifest symlink that leaves the layer directory
// or names an absolute target fails the layer's ingest with the
// source-unreachable sentinel rather than reading as an absent manifest and
// silently dropping the package. A sole SKILL.md makes a package, so its
// symlink is held to the same rule as an ARTIFACT.md.
func TestSourceIngest_EscapingManifestFailsTheLayer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// link places the manifest symlink under root; parent holds the
		// files outside the layer.
		link func(t *testing.T, parent, root string)
	}{
		{name: "escaping ARTIFACT.md", link: func(t *testing.T, parent, root string) {
			writeOutside(t, parent, "ARTIFACT.md", agentArtifact("outside"))
			symlink(t, filepath.Join("..", "..", "ARTIFACT.md"), filepath.Join(root, "x", "ARTIFACT.md"))
		}},
		{name: "escaping sole SKILL.md", link: func(t *testing.T, parent, root string) {
			writeOutside(t, parent, "SKILL.md", skillBody("y"))
			symlink(t, filepath.Join("..", "..", "SKILL.md"), filepath.Join(root, "y", "SKILL.md"))
		}},
		{name: "escaping nested ARTIFACT.md below a package", link: func(t *testing.T, parent, root string) {
			writeOutside(t, parent, "ARTIFACT.md", agentArtifact("outside"))
			symlink(t, filepath.Join("..", "..", "..", "..", "ARTIFACT.md"), filepath.Join(root, "finance", "pay-invoice", "sub", "ARTIFACT.md"))
		}},
		{name: "absolute-target sole SKILL.md", link: func(t *testing.T, _, root string) {
			target := filepath.Join(root, "inside", "skill.txt")
			if err := os.WriteFile(target, []byte(skillBody("z")), 0o644); err != nil {
				t.Fatalf("WriteFile inside target: %v", err)
			}
			symlink(t, target, filepath.Join(root, "z", "SKILL.md"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			parent := t.TempDir()
			root := filepath.Join(parent, "layer")
			testharness.WriteTree(t, root,
				testharness.WriteTreeOption{Path: "finance/pay-invoice/ARTIFACT.md", Content: agentArtifact("Pay an invoice")},
				testharness.WriteTreeOption{Path: "inside/README.txt", Content: "inside\n"},
			)
			for _, dir := range []string{"x", "y", "z", "finance/pay-invoice/sub"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
			}
			tc.link(t, parent, root)
			st := newStore(t)
			_, err := ingest.Ingest(ctx, st, ingest.Request{
				TenantID: "tenant-1",
				LayerID:  "team-shared",
				Files:    source.ConfinedFS(root),
			})
			if err == nil {
				t.Fatalf("Ingest accepted a layer whose manifest symlink leaves the confinement")
			}
			if !errors.Is(err, source.ErrSourceUnreachable) {
				t.Errorf("error %q is not source.ErrSourceUnreachable", err)
			}
			manifests, err := st.ListManifests(ctx, "tenant-1")
			if err != nil {
				t.Fatalf("ListManifests: %v", err)
			}
			if len(manifests) != 0 {
				t.Errorf("refused ingest persisted %d artifacts, want 0", len(manifests))
			}
		})
	}
}

// Spec: §7.3.1, §4.2 — a dangling SKILL.md symlink is an absent manifest
// (fs.ErrNotExist), so its directory is not a package and the rest of the
// layer ingests.
func TestSourceIngest_DanglingSkillManifestIsAbsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "layer")
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "finance/pay-invoice/ARTIFACT.md", Content: agentArtifact("Pay an invoice")},
	)
	if err := os.MkdirAll(filepath.Join(root, "w"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	symlink(t, "missing.md", filepath.Join(root, "w", "SKILL.md"))
	st := newStore(t)
	if _, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "team-shared",
		Files:    source.ConfinedFS(root),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	manifests, err := st.ListManifests(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	for _, m := range manifests {
		if m.ArtifactID == "w" {
			t.Errorf("a dangling SKILL.md symlink stored a row for w")
		}
	}
	if len(manifests) != 1 {
		t.Errorf("stored %d manifests, want the one finance/pay-invoice row", len(manifests))
	}
}

func writeOutside(t *testing.T, parent, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parent, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile outside %s: %v", name, err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
}
