package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer/source"
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
		testharness.WriteTreeOption{Path: "finance/pay-invoice/ARTIFACT.md", Content: contextArtifact("Pay an invoice")},
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
// source-unreachable sentinel. The domain walk commits ahead of the artifact
// walk, so the domain record is present while no artifact is.
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
			PublishEvent: func(_ context.Context, eventType string, _ map[string]any) {
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
	if len(domains) != 1 {
		t.Fatalf("want the layer's domain record, got %d", len(domains))
	}
	firstPublished := len(published)
	firstAudited := countDomainPublished(audited)

	// A second refused cycle over the same unchanged tree keeps the domain
	// record and emits no further domain.published on either seam.
	if _, err := ingest.Ingest(ctx, st, req()); !errors.Is(err, source.ErrSourceUnreachable) {
		t.Fatalf("second cycle: want ErrSourceUnreachable, got %v", err)
	}
	domains, err = st.ListDomains(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1 {
		t.Errorf("second cycle: want 1 domain record, got %d", len(domains))
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
// link keeps the source-unreachable sentinel, and an absent SKILL.md keeps the
// existing missing-file message.
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
	_, err = ingest.Ingest(ctx, newStore(t), ingest.Request{
		TenantID: "tenant-1",
		LayerID:  "team-shared",
		Files:    source.ConfinedFS(absentRoot),
	})
	if err == nil {
		t.Fatalf("Ingest accepted a skill with no SKILL.md")
	}
	if errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("an absent SKILL.md must not be classified unreachable: %v", err)
	}
	if !strings.Contains(err.Error(), "type: skill missing SKILL.md") {
		t.Errorf("absent SKILL.md message changed: %v", err)
	}
}
