package e2e

// Runtime layer republish and multi-version fixtures.
//
// The standalone harness ingests each layer once at boot from a static
// filesystem fixture, so staging a second version of an artifact, a deprecated
// successor, or a mid-session republish was unbuildable end to end. The
// managed-stack parity work (standard_stack_parity_test.go) added
// msPublishGitLayer, which registers a git source and reingests once against a
// live standard-mode server; that is the same primitive in a narrow,
// live-infra-only form.
//
// This file generalizes runtime publish into "publish version N of a layer"
// against the common standalone harness, with no external infrastructure. A
// republishLayer registers one local-source layer at runtime through `podium
// layer register --local`, then each publishVersion call rewrites the layer's
// on-disk artifact at the requested version (optionally deprecated, optionally
// with a replaced_by successor and extra bundled files) and triggers `podium
// layer reingest`. The standalone reingest runner resolves the local source
// provider and runs the §7.3.1 ingest pipeline, so a version bump records a new
// immutable manifest version under the same canonical id while prior versions
// remain loadable. This lets the pin-stability, deprecation, version-selection,
// and session-snapshot journeys stage multiple versions.
//
// Why local source rather than git: a standalone server can read a client-side
// --local path (it shares the filesystem), so the publish loop is a single file
// rewrite plus a reingest, with no per-version commit. msPublishGitLayer keeps
// the git path for the remote standard-mode server, which cannot read a client
// path. Both drive the same ingest pipeline (internal/serverboot/reingest.go),
// so the observable result is identical.
//
// Spec: §7.3.1 (manual reingest runs the ingest pipeline for a resolved layer),
// §4.7.6 (a version is immutable; `latest` resolves to the most recent
// non-deprecated version), §7.3.2 (a manifest that sets deprecated: true
// supersedes a prior non-deprecated version), §4.6/§13.10 (a standalone
// deployment has no identity provider, so the local operator is the de facto
// admin and may register and reingest layers).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// republishLayer is a handle to a local-source layer registered at runtime
// against a running server. publishVersion writes the layer's artifact at a
// given version and reingests, so successive calls stage multiple versions of
// one canonical id.
type republishLayer struct {
	srv     *serverProc
	layerID string
	dir     string
}

// versionSpec describes one published version of a single artifact. ID is the
// canonical artifact id (a slash path under the layer root). Version is the
// semver the manifest carries. Description feeds the search index and the
// load_artifact envelope. Deprecated sets the §7.3.2 deprecated flag on this
// version so latest resolution skips it. ReplacedBy records the successor id
// surfaced in the deprecation advisory. Files adds further bundled files in the
// artifact directory (for example a SKILL.md body or a script), keyed by path
// relative to the artifact directory.
//
// Extends, WhenToUse, and Sensitivity carry the §4.6 extends-merge inputs so a
// version can declare a pinned parent (Extends: "<id>@<pin>"), inherited
// when_to_use entries, and a sensitivity the merge folds most-restrictively.
// They are emitted into the frontmatter only when set, so a plain artifact
// carries none of them.
type versionSpec struct {
	ID          string
	Version     string
	Description string
	Type        string
	Deprecated  bool
	ReplacedBy  string
	Extends     string
	WhenToUse   []string
	Sensitivity string
	Files       map[string]string
}

// newRepublishLayer registers an empty local-source layer named layerID at
// runtime against srv via `podium layer register --local <dir>` and returns a
// handle whose publishVersion stages versions into it. The layer's on-disk
// directory starts empty; the first publishVersion writes the artifact and
// reingests. A standalone server resolves the anonymous caller to the de facto
// admin, so the admin-defined registration is accepted with no token (§13.10).
func newRepublishLayer(t testing.TB, srv *serverProc, layerID string) *republishLayer {
	t.Helper()
	dir := t.TempDir()
	rr := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", layerID, "--local", dir)
	if rr.Exit != 0 {
		t.Fatalf("layer register %q exit=%d stderr=%s stdout=%s", layerID, rr.Exit, rr.Stderr, rr.Stdout)
	}
	return &republishLayer{srv: srv, layerID: layerID, dir: dir}
}

// publishVersion writes spec's artifact into the layer directory at the
// requested version and triggers a reingest, then polls load_artifact until the
// new version is resolvable (bounded, never blocking). It returns the reingest
// CLI output. Calling it again with a higher version stages an additional
// immutable version under the same id; prior versions stay loadable via
// load_artifact?version=. A version that sets Deprecated is skipped by latest
// resolution (§4.7.6/§7.3.2).
//
// The artifact is rewritten in place (the directory holds one path per id), so
// the layer always presents the union of every id published into it at its most
// recently written bytes. To stage two coexisting versions of the same id,
// publish the lower version first and the higher version second; the ingest
// pipeline records each as a distinct manifest version keyed by (id, version).
func (l *republishLayer) publishVersion(t testing.TB, spec versionSpec) cliResult {
	t.Helper()
	if spec.ID == "" || spec.Version == "" {
		t.Fatalf("publishVersion requires ID and Version (got %+v)", spec)
	}
	l.writeArtifact(t, spec)
	ri := runPodium(t, "", nil, "layer", "reingest", "--registry", l.srv.BaseURL, l.layerID)
	if ri.Exit != 0 {
		t.Fatalf("layer reingest %q (id=%s version=%s) exit=%d stderr=%s stdout=%s",
			l.layerID, spec.ID, spec.Version, ri.Exit, ri.Stderr, ri.Stdout)
	}
	// The standalone reingest runs synchronously, but an external vector backend
	// drains the index through the §4.7.2 outbox, so poll the control plane until
	// the exact version is resolvable rather than racing a single immediate read.
	if !l.waitForVersion(t, spec.ID, spec.Version, 15*time.Second) {
		t.Fatalf("version %s of %s not resolvable after reingest of layer %q\nreingest stdout: %s\nserver log:\n%s",
			spec.Version, spec.ID, l.layerID, ri.Stdout, l.srv.log())
	}
	return ri
}

// writeArtifact materializes spec's artifact under the layer directory: an
// ARTIFACT.md with the Podium frontmatter (type, version, description, and the
// optional deprecated / replaced_by fields) plus any extra bundled Files. The
// directory is created fresh each call so a rewrite at a new version fully
// replaces the prior bytes for that id.
func (l *republishLayer) writeArtifact(t testing.TB, spec versionSpec) {
	t.Helper()
	artDir := filepath.Join(l.dir, filepath.FromSlash(spec.ID))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", artDir, err)
	}
	if err := os.WriteFile(filepath.Join(artDir, "ARTIFACT.md"), []byte(republishArtifact(spec)), 0o644); err != nil {
		t.Fatalf("write ARTIFACT.md for %s@%s: %v", spec.ID, spec.Version, err)
	}
	for rel, content := range spec.Files {
		p := filepath.Join(artDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write bundled file %s for %s@%s: %v", rel, spec.ID, spec.Version, err)
		}
	}
}

// loadVersion GETs load_artifact for the given id and version (empty version
// resolves latest) and returns the HTTP status and decoded envelope. It is the
// read-side companion to publishVersion: assert which version a caller resolves.
func (l *republishLayer) loadVersion(t testing.TB, id, version string) (int, exLoadResp) {
	t.Helper()
	url := l.srv.BaseURL + "/v1/load_artifact?id=" + id
	if version != "" {
		url += "&version=" + version
	}
	st, body := getRaw(t, url)
	var r exLoadResp
	if st == 200 {
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("decode load_artifact %s@%s: %v\nbody: %s", id, version, err, body)
		}
	}
	return st, r
}

// waitForVersion polls load_artifact?version= until the exact version resolves
// with HTTP 200 or the deadline elapses, returning whether it became
// resolvable. A deprecated version is still addressable by explicit version, so
// this works for deprecated publishes too.
func (l *republishLayer) waitForVersion(t testing.TB, id, version string, within time.Duration) bool {
	t.Helper()
	url := l.srv.BaseURL + "/v1/load_artifact?id=" + id + "&version=" + version
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if st, _ := getRaw(t, url); st == 200 {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// republishArtifact renders the ARTIFACT.md for a versionSpec. The type
// defaults to context (the registry indexes the description for search and
// context artifacts carry their body inline, so a single ARTIFACT.md is a
// complete, lint-clean artifact). deprecated and replaced_by are emitted only
// when set so a non-deprecated version carries no deprecation frontmatter.
func republishArtifact(spec versionSpec) string {
	typ := spec.Type
	if typ == "" {
		typ = "context"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: %s\nversion: %s\n", typ, spec.Version)
	if spec.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", spec.Description)
	}
	if spec.Extends != "" {
		fmt.Fprintf(&b, "extends: %s\n", spec.Extends)
	}
	if spec.Sensitivity != "" {
		fmt.Fprintf(&b, "sensitivity: %s\n", spec.Sensitivity)
	}
	if len(spec.WhenToUse) > 0 {
		b.WriteString("when_to_use:\n")
		for _, w := range spec.WhenToUse {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	if spec.Deprecated {
		b.WriteString("deprecated: true\n")
	}
	if spec.ReplacedBy != "" {
		fmt.Fprintf(&b, "replaced_by: %s\n", spec.ReplacedBy)
	}
	b.WriteString("---\n\n")
	body := spec.Description
	if body == "" {
		body = spec.ID
	}
	fmt.Fprintf(&b, "%s body (v%s).\n", body, spec.Version)
	return b.String()
}
