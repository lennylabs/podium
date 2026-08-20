package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// parentContentHash is the stored content hash of a parent version, in the
// 64-hex form §4.7.6 requires of a content-hash pin.
func parentContentHash(ver string) string {
	sum := sha256.Sum256([]byte(ver))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// putParentVersion stores one candidate version of the parent artifact the
// extends tests resolve against.
func putParentVersion(t *testing.T, st store.Store, ver string, deprecated bool, ingestedAt time.Time) {
	t.Helper()
	rec := store.ManifestRecord{
		TenantID:    "default",
		ArtifactID:  "shared/parent",
		Version:     ver,
		ContentHash: parentContentHash(ver),
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

// Spec: §4.6 — an exact pin naming a deprecated parent version is refused, and
// the message names deprecation rather than reporting an unsatisfiable range.
func TestResolveExtendsPin_ExactPinOntoDeprecatedIsRefused(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.0.1",
		"finance/child", "1.0.0", "finance")
	if err == nil {
		t.Fatal("resolveExtendsPin = nil error, want a refusal of the deprecated parent version")
	}
	if strings.Contains(err.Error(), "no parent version satisfies") {
		t.Errorf("err = %v, want the exact pin to keep its deprecated candidate rather than read as a range miss", err)
	}
	if !strings.Contains(err.Error(), "shared/parent@1.0.1 is deprecated") {
		t.Errorf("err = %v, want a message naming the deprecated parent version", err)
	}
}

// Spec: §4.6 — a content-hash pin names one version too, so a hash that
// resolves to a deprecated version is refused on the same rule. The hash arm
// resolves through the stored content hashes rather than through
// version.Resolve, so it carries its own path to the refusal.
func TestResolveExtendsPin_ContentHashPinOntoDeprecatedIsRefused(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	ref := "shared/parent@" + parentContentHash("1.0.1")
	_, _, _, err := resolveExtendsPin(ctx, st, "default", ref,
		"finance/child", "1.0.0", "finance")
	if err == nil {
		t.Fatal("resolveExtendsPin = nil error, want a refusal of the deprecated parent version")
	}
	if !strings.Contains(err.Error(), "shared/parent@1.0.1 is deprecated") {
		t.Errorf("err = %v, want a message naming the deprecated parent version", err)
	}
}

// Spec: §4.7.6 — a content-hash pin onto a live version still resolves, so the
// refusal above turns on the deprecation flag rather than on the pin form.
func TestResolveExtendsPin_ContentHashPinResolvesLiveVersion(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	ref := "shared/parent@" + parentContentHash("1.0.0")
	pin, _, _, err := resolveExtendsPin(ctx, st, "default", ref,
		"finance/child", "1.0.0", "finance")
	if err != nil {
		t.Fatalf("resolveExtendsPin: %v", err)
	}
	if pin != "shared/parent@1.0.0" {
		t.Errorf("pin = %q, want shared/parent@1.0.0", pin)
	}
}

// Spec: §4.7.6 — a range reference whose candidates are all deprecated is
// refused, and the message names deprecation so the report does not send the
// author looking for a publication that already happened.
func TestResolveExtendsPin_RangeWithOnlyDeprecatedCandidatesIsRefused(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", true, base)
	putParentVersion(t, st, "1.0.1", true, base.Add(time.Hour))

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.x",
		"finance/child", "1.0.0", "finance")
	if err == nil {
		t.Fatal("resolveExtendsPin = nil error, want a refusal when every candidate is deprecated")
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("err = %v, want a message naming deprecation", err)
	}
	if strings.Contains(err.Error(), "ingested yet") || strings.Contains(err.Error(), "no parent version satisfies") {
		t.Errorf("err = %v, want the deprecation arm rather than an unpublished-parent or range-miss report", err)
	}
}

// Spec: §4.7.6 — an unpinned reference takes the same refusal, because
// `latest` selects among non-deprecated versions only and the filter leaves
// nothing behind.
func TestResolveExtendsPin_UnpinnedWithOnlyDeprecatedCandidatesIsRefused(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", true, base)

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent",
		"finance/child", "1.0.0", "finance")
	if err == nil {
		t.Fatal("resolveExtendsPin = nil error, want a refusal when every candidate is deprecated")
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("err = %v, want a message naming deprecation", err)
	}
}

// Spec: §4.7.6 — a range that matches no stored version keeps the generic
// unsatisfied-range message when a live candidate exists. The deprecation
// refusal is a separate arm, so an ordinary range miss is never reported as a
// deprecated parent line.
func TestResolveExtendsPin_RangeMissWithLiveCandidateKeepsUnsatisfiedMessage(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putParentVersion(t, st, "1.0.0", false, base)

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@2.x",
		"finance/child", "1.0.0", "finance")
	if err == nil {
		t.Fatal("resolveExtendsPin = nil error, want an unsatisfied-range refusal")
	}
	if !strings.Contains(err.Error(), `no parent version satisfies "shared/parent@2.x"`) {
		t.Errorf("err = %v, want the generic unsatisfied-range message", err)
	}
	if strings.Contains(err.Error(), "deprecated") {
		t.Errorf("err = %v, want no mention of deprecation for a range miss against a live candidate", err)
	}
}

// Spec: §4.6 — the refusal fires only when the parent is deprecated, so a
// parent with no stored version still reports that it was never ingested.
func TestResolveExtendsPin_MissingParentKeepsNotIngestedMessage(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()

	_, _, _, err := resolveExtendsPin(ctx, st, "default", "shared/parent@1.x",
		"finance/child", "1.0.0", "finance")
	if err == nil || !strings.Contains(err.Error(), "ingested yet") {
		t.Errorf("err = %v, want the unpublished-parent message", err)
	}
}
