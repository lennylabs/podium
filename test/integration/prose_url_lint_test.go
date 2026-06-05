package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.4 — the ingest-time linter validates prose URL
// references with an HTTP HEAD (200/3xx); "Drift between manifest text and
// bundled files is an ingest error." Before the fix the URL check was gated
// behind a nil HTTPClient in every production ingest path, so URL references
// were never validated. This drives the real ingest pipeline with a wired
// client (as the server/CLI now do) and asserts a 404 URL blocks its
// artifact at error severity while a 200 URL passes.
func TestIngest_ProseURLHeadValidated(t *testing.T) {
	t.Parallel()

	var heads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	live := "---\ntype: context\nversion: 1.0.0\ndescription: live\nsensitivity: low\n---\n\nSee [policy](" + ts.URL + "/ok).\n"
	dead := "---\ntype: context\nversion: 1.0.0\ndescription: dead\nsensitivity: low\n---\n\nSee [policy](" + ts.URL + "/missing).\n"
	files := fstest.MapFS{
		"team/live/ARTIFACT.md": &fstest.MapFile{Data: []byte(live)},
		"team/dead/ARTIFACT.md": &fstest.MapFile{Data: []byte(dead)},
	}

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files:    files,
		// The wired client is what the server and CLI now supply by default.
		Linter: &lint.Linter{HTTPClient: ts.Client()},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if heads.Load() < 2 {
		t.Errorf("HEAD probes = %d, want at least 2 (one per URL reference)", heads.Load())
	}
	// The dead URL artifact is blocked by lint at error severity.
	deadBlocked := false
	for _, d := range res.LintFailures {
		if d.ArtifactID == "team/dead" && d.Code == "lint.prose_reference" && strings.Contains(d.Message, "/missing") {
			deadBlocked = true
		}
	}
	if !deadBlocked {
		t.Errorf("dead URL artifact should be a lint failure, got LintFailures=%v", res.LintFailures)
	}
	// The live URL artifact ingests cleanly.
	for _, d := range res.LintFailures {
		if d.ArtifactID == "team/live" {
			t.Errorf("live URL artifact should not fail lint: %v", d)
		}
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (only team/live)", res.Accepted)
	}
}

// Spec: §4.4 line 348 — the ingest-time linter resolves a prose
// reference that names another artifact against the current visible catalog.
// A reference to a sibling artifact ID ingests cleanly; a reference to an
// artifact ID absent from the catalog is an ingest error (§4.4 line 350).
func TestIngest_ProseReferenceResolvesAgainstCatalog(t *testing.T) {
	t.Parallel()

	payInvoice := "---\ntype: context\nversion: 1.0.0\ndescription: pay invoice\nsensitivity: low\n---\n\nSee [the reconciler](finance/ap/reconcile).\n"
	reconcile := "---\ntype: context\nversion: 1.0.0\ndescription: reconcile\nsensitivity: low\n---\n\nNo references here.\n"
	broken := "---\ntype: context\nversion: 1.0.0\ndescription: broken\nsensitivity: low\n---\n\nSee [ghost](finance/ap/does-not-exist).\n"
	files := fstest.MapFS{
		"finance/ap/pay-invoice/ARTIFACT.md": &fstest.MapFile{Data: []byte(payInvoice)},
		"finance/ap/reconcile/ARTIFACT.md":   &fstest.MapFile{Data: []byte(reconcile)},
		"finance/ap/broken/ARTIFACT.md":      &fstest.MapFile{Data: []byte(broken)},
	}

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files:    files,
		// Offline: artifact-reference resolution does not need the URL probe.
		Linter: lint.NewIngestLinter(true),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// The cross-artifact reference resolves; pay-invoice and reconcile ingest.
	for _, d := range res.LintFailures {
		if d.ArtifactID == "finance/ap/pay-invoice" {
			t.Errorf("reference to a catalog artifact should resolve, got %v", d)
		}
	}
	// The unknown artifact reference is blocked at error severity.
	brokenBlocked := false
	for _, d := range res.LintFailures {
		if d.ArtifactID == "finance/ap/broken" && d.Code == "lint.prose_reference" &&
			strings.Contains(d.Message, "finance/ap/does-not-exist") {
			brokenBlocked = true
		}
	}
	if !brokenBlocked {
		t.Errorf("unknown artifact reference should be a lint failure, got %v", res.LintFailures)
	}
	if res.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2 (pay-invoice and reconcile)", res.Accepted)
	}
}

// Spec: §4.4 line 347 — a URL reference is valid only on HEAD 200
// or 3xx. A 204 No Content does not confirm the named resource, so it blocks
// the artifact at ingest even though it is a 2xx status.
func TestIngest_ProseURLRejectsNonOK2xx(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nc") {
			w.WriteHeader(http.StatusNoContent) // 204
			return
		}
		w.WriteHeader(http.StatusOK) // 200
	}))
	defer ts.Close()

	ncArt := "---\ntype: context\nversion: 1.0.0\ndescription: noContent\nsensitivity: low\n---\n\nSee [policy](" + ts.URL + "/nc).\n"
	okArt := "---\ntype: context\nversion: 1.0.0\ndescription: ok\nsensitivity: low\n---\n\nSee [policy](" + ts.URL + "/ok).\n"
	files := fstest.MapFS{
		"team/nc/ARTIFACT.md": &fstest.MapFile{Data: []byte(ncArt)},
		"team/ok/ARTIFACT.md": &fstest.MapFile{Data: []byte(okArt)},
	}

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files:    files,
		Linter:   &lint.Linter{HTTPClient: ts.Client()},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	blocked := false
	for _, d := range res.LintFailures {
		if d.ArtifactID == "team/nc" && d.Code == "lint.prose_reference" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("a 204 URL HEAD should block the artifact, got LintFailures=%v", res.LintFailures)
	}
	// The 200 URL artifact still passes, so the 2xx tightening is narrow.
	for _, d := range res.LintFailures {
		if d.ArtifactID == "team/ok" {
			t.Errorf("a 200 URL HEAD should pass, got %v", d)
		}
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (only team/ok)", res.Accepted)
	}
}

// Spec: §4.4 — the offline opt-out (PODIUM_INGEST_OFFLINE /
// --offline, modeled here by NewIngestLinter(true)) skips the URL HEAD
// probe so a dead URL no longer blocks ingest; the bundled-file existence
// check still runs.
func TestIngest_ProseURLOfflineSkipsProbe(t *testing.T) {
	t.Parallel()

	var heads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	dead := "---\ntype: context\nversion: 1.0.0\ndescription: dead\nsensitivity: low\n---\n\nSee [policy](" + ts.URL + "/missing).\n"
	files := fstest.MapFS{"team/dead/ARTIFACT.md": &fstest.MapFile{Data: []byte(dead)}}

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files:    files,
		Linter:   lint.NewIngestLinter(true), // offline
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if heads.Load() != 0 {
		t.Errorf("offline ingest must not probe URLs, got %d HEADs", heads.Load())
	}
	if res.Accepted != 1 || len(res.LintFailures) != 0 {
		t.Errorf("offline ingest should accept the artifact, got Accepted=%d LintFailures=%v", res.Accepted, res.LintFailures)
	}
}
