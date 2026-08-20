package ingest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// putParentVersion stores one candidate version of the parent artifact the
// extends tests resolve against.
func putParentVersion(t *testing.T, st store.Store, ver string, deprecated bool, ingestedAt time.Time) {
	t.Helper()
	rec := store.ManifestRecord{
		TenantID:    "default",
		ArtifactID:  "shared/parent",
		Version:     ver,
		ContentHash: "sha256:" + ver,
		Type:        "agent",
		Layer:       "shared",
		Deprecated:  deprecated,
		IngestedAt:  ingestedAt,
		Frontmatter: []byte("---\ntype: agent\nversion: " + ver + "\n---\n"),
	}
	if err := st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s: %v", ver, err)
	}
}

// Spec: §4.7.6 — a range reference selects among the parent's non-deprecated
// versions, so a deprecated candidate is skipped and the live one is pinned.
func TestResolveExtendsPin_RangeSkipsDeprecatedCandidate(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	pin, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.x",
		"finance/child", "1.0.0", "finance")
	if err != nil {
		t.Fatalf("resolveExtendsPin: %v", err)
	}
	if pin != "shared/parent@1.0.0" {
		t.Errorf("pin = %q, want shared/parent@1.0.0 (the deprecated 1.0.1 must be skipped)", pin)
	}
}

// Spec: §4.7.6 — an unpinned reference resolves `latest` to the most recently
// ingested non-deprecated version, which is neither the highest semver nor the
// most recent ingest overall.
func TestResolveExtendsPin_UnpinnedSelectsMostRecentlyIngestedNonDeprecated(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "2.0.0", false, base)
	putParentVersion(t, st, "1.1.0", false, base.Add(24*time.Hour))
	putParentVersion(t, st, "3.0.0", true, base.Add(48*time.Hour))

	pin, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent",
		"finance/child", "1.0.0", "finance")
	if err != nil {
		t.Fatalf("resolveExtendsPin: %v", err)
	}
	if pin != "shared/parent@1.1.0" {
		t.Errorf("pin = %q, want shared/parent@1.1.0 (backport ingested last, 3.0.0 deprecated)", pin)
	}
}

// Spec: §4.7.6 — an exact pin against a live candidate set resolves to the
// version the reference names, and carries the parent's type.
func TestResolveExtendsPin_ExactPinResolvesLiveVersion(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", false, base.Add(time.Hour))

	pin, parentType, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.0.0",
		"finance/child", "1.0.0", "finance")
	if err != nil {
		t.Fatalf("resolveExtendsPin: %v", err)
	}
	if pin != "shared/parent@1.0.0" || parentType != "agent" {
		t.Errorf("pin = %q, type = %q, want shared/parent@1.0.0 and agent", pin, parentType)
	}
}

// Spec: §4.7.6 — the deprecation filter covers a range or unpinned reference
// only. An exact pin naming a deprecated version keeps its own candidate, so
// the failure it reports names deprecation rather than an unsatisfiable range.
func TestResolveExtendsPin_ExactPinOntoDeprecatedIsNotARangeMiss(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.0.1",
		"finance/child", "1.0.0", "finance")
	if err != nil && strings.Contains(err.Error(), "no parent version satisfies") {
		t.Errorf("err = %v, want the exact pin to keep its deprecated candidate rather than read as a range miss", err)
	}
}
