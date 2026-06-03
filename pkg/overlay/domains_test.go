package overlay

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.5.1 / §6.4 (F-4.5.2, F-6.4.2) — ResolveDomains surfaces the overlay
// DOMAIN.md set keyed by canonical domain path so the consumer that exposes
// load_domain can apply the §4.5.4 client-side merge.
func TestResolveDomains_Populated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "DOMAIN.md", Content: "---\ndescription: Root\n---\n"},
		testharness.WriteTreeOption{Path: "finance/DOMAIN.md", Content: "---\ndescription: Finance\ndiscovery:\n  keywords:\n    - money\n---\n\nBody\n"},
		testharness.WriteTreeOption{Path: "finance/x/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n"},
	)
	got, err := (Filesystem{Path: root}).ResolveDomains(context.Background())
	if err != nil {
		t.Fatalf("ResolveDomains: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d domains, want 2 (root + finance): %v", len(got), got)
	}
	if got["finance"] == nil || got["finance"].Description != "Finance" {
		t.Errorf("finance domain = %+v, want description Finance", got["finance"])
	}
	if got["finance"].Discovery == nil || len(got["finance"].Discovery.Keywords) != 1 {
		t.Errorf("finance keywords not parsed: %+v", got["finance"])
	}
}

// Spec: §6.4 — empty/unset path returns ErrNoOverlay, like Resolve.
func TestResolveDomains_EmptyPath(t *testing.T) {
	t.Parallel()
	_, err := (Filesystem{}).ResolveDomains(context.Background())
	if !errors.Is(err, ErrNoOverlay) {
		t.Errorf("got %v, want ErrNoOverlay", err)
	}
}

// Spec: §6.4 — an overlay with artifacts but no DOMAIN.md yields an empty map
// (no error), so the merge has nothing to compose.
func TestResolveDomains_NoDomainFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n"},
	)
	got, err := (Filesystem{Path: root}).ResolveDomains(context.Background())
	if err != nil {
		t.Fatalf("ResolveDomains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d domains, want 0", len(got))
	}
}
