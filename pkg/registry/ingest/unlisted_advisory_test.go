package ingest_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// ingestDomain ingests a single-layer snapshot containing one DOMAIN.md and
// one artifact under it against st, returning the result. Reusing the same
// st across calls exercises the §12 "newly-set" comparison against the prior
// ingested DOMAIN.md.
func ingestDomain(t *testing.T, st store.Store, domainMD string) *ingest.Result {
	t.Helper()
	mfs := fstest.MapFS{
		"finance/DOMAIN.md":          &fstest.MapFile{Data: []byte(domainMD)},
		"finance/ap/run/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("AP run"))},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L1", Files: mfs,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

const (
	domainListed   = "---\ndescription: Finance\n---\n\n# Finance\n"
	domainUnlisted = "---\nunlisted: true\ndescription: Finance\n---\n\n# Finance\n"
)

// Spec: §12 (F-12.0.1) — "ingest-time lint flags newly-set unlisted: true for
// review." A DOMAIN.md that flips from listed to unlisted across two ingests
// is flagged on the ingest that sets it; the prior listed ingest is not.
func TestIngest_NewlyUnlistedFlagged(t *testing.T) {
	t.Parallel()
	st := newStore(t)

	// Ingest 1: listed — no advisory.
	first := ingestDomain(t, st, domainListed)
	if hasAdvisory(first, "lint.domain_newly_unlisted", "finance") {
		t.Fatalf("listed DOMAIN.md flagged as newly-unlisted: %+v", first.Advisories)
	}

	// Ingest 2: now unlisted — flagged.
	second := ingestDomain(t, st, domainUnlisted)
	if !hasAdvisory(second, "lint.domain_newly_unlisted", "finance") {
		t.Fatalf("newly-set unlisted: true not flagged: %+v", second.Advisories)
	}
	// Advisory, not gating: the artifact under it still ingests.
	if second.Accepted == 0 && second.Idempotent == 0 {
		t.Errorf("unlisted advisory must not gate ingest: %+v", second)
	}
	for _, d := range second.Advisories {
		if d.Code == "lint.domain_newly_unlisted" {
			if d.Severity != "warning" {
				t.Errorf("severity = %q, want warning", d.Severity)
			}
			if !strings.Contains(d.Message, "unlisted") {
				t.Errorf("message missing 'unlisted': %s", d.Message)
			}
		}
	}
}

// Spec: §12 (F-12.0.1) — a re-ingest of an already-unlisted DOMAIN.md is not
// newly-set, so it draws no advisory; the review signal fires once per change.
func TestIngest_AlreadyUnlistedNotReflagged(t *testing.T) {
	t.Parallel()
	st := newStore(t)

	if hasAdvisory(ingestDomain(t, st, domainUnlisted), "lint.domain_newly_unlisted", "finance") != true {
		// First ingest of a brand-new unlisted domain is newly-set (no prior
		// listed version), so it is flagged.
		t.Fatalf("first ingest of an unlisted DOMAIN.md should be flagged")
	}
	// Steady-state re-ingest of the identical unlisted DOMAIN.md: quiet.
	again := ingestDomain(t, st, domainUnlisted)
	if hasAdvisory(again, "lint.domain_newly_unlisted", "finance") {
		t.Errorf("steady-state re-ingest reflagged an already-unlisted domain: %+v", again.Advisories)
	}
}

// Spec: §12 (F-12.0.1) — a brand-new DOMAIN.md that ships unlisted: true is
// newly-set (there is no prior listed version), so it is flagged on first
// ingest.
func TestIngest_BrandNewUnlistedFlagged(t *testing.T) {
	t.Parallel()
	res := ingestDomain(t, newStore(t), domainUnlisted)
	if !hasAdvisory(res, "lint.domain_newly_unlisted", "finance") {
		t.Errorf("brand-new unlisted DOMAIN.md not flagged: %+v", res.Advisories)
	}
}

// A listed DOMAIN.md never draws the advisory, even across repeated ingests.
func TestIngest_ListedNeverFlagged(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	for i := 0; i < 2; i++ {
		res := ingestDomain(t, st, domainListed)
		if hasAdvisory(res, "lint.domain_newly_unlisted", "finance") {
			t.Fatalf("listed DOMAIN.md flagged on ingest %d: %+v", i+1, res.Advisories)
		}
	}
}
