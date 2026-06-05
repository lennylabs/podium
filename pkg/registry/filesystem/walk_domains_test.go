package filesystem

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

const domainMD = `---
description: %s
---

%s
`

// Spec: §4.5.1 / §6.4 — WalkDomains discovers every
// DOMAIN.md under a layer, keyed by canonical domain path, including the
// root-level DOMAIN.md (path "") which the artifact walk would reject. The
// artifact walk (Walk) continues to skip DOMAIN.md.
func TestWalkDomains_DiscoversDomainFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "DOMAIN.md", Content: stringf(domainMD, "Root domain", "Root body")},
		testharness.WriteTreeOption{Path: "finance/DOMAIN.md", Content: stringf(domainMD, "Finance", "Finance body")},
		testharness.WriteTreeOption{Path: "finance/ap/DOMAIN.md", Content: stringf(domainMD, "Accounts Payable", "AP body")},
		// An artifact package present alongside a domain must not be picked up.
		testharness.WriteTreeOption{Path: "finance/ap/pay/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "finance/ap/pay/SKILL.md", Content: stringf(skillBody, "pay", "Pay", "pay")},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recs, err := reg.WalkDomains()
	if err != nil {
		t.Fatalf("WalkDomains: %v", err)
	}
	got := map[string]string{}
	for _, r := range recs {
		got[r.Path] = r.Domain.Description
	}
	want := map[string]string{
		"":           "Root domain",
		"finance":    "Finance",
		"finance/ap": "Accounts Payable",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d domains %v, want %d", len(got), got, len(want))
	}
	for path, desc := range want {
		if got[path] != desc {
			t.Errorf("domain %q description = %q, want %q", path, got[path], desc)
		}
	}
	// The root body is retained for the requested-domain rendering.
	for _, r := range recs {
		if r.Path == "finance" && r.Domain.Body == "" {
			t.Errorf("finance domain body not captured")
		}
	}
}

// Spec: §4.5.1 — a malformed DOMAIN.md is skipped (the linter reports it at
// ingest), matching mergedDomains and the lint walker.
func TestWalkDomains_SkipsMalformed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ok/DOMAIN.md", Content: stringf(domainMD, "OK", "body")},
		// include with an invalid recursive glob form the parser rejects.
		testharness.WriteTreeOption{Path: "bad/DOMAIN.md", Content: "---\ninclude:\n  - \"finance/**/\"\nunlisted: notabool\n---\n"},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recs, err := reg.WalkDomains()
	if err != nil {
		t.Fatalf("WalkDomains: %v", err)
	}
	for _, r := range recs {
		if r.Path == "bad" {
			t.Errorf("malformed DOMAIN.md was not skipped: %+v", r)
		}
	}
}

// Spec: §6.4 — an empty layer surfaces no DOMAIN.md records (and WalkDomains
// does not error).
func TestWalkDomains_EmptyLayer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "greetings/hi/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "greetings/hi/SKILL.md", Content: stringf(skillBody, "hi", "Hi", "hi")},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recs, err := reg.WalkDomains()
	if err != nil {
		t.Fatalf("WalkDomains: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("WalkDomains = %d records, want 0", len(recs))
	}
}
