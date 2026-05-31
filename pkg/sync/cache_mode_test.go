package sync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// newErrorRegistry serves a fixed non-2xx status + body on every route, so a
// sync against it reaches the server and receives a structured rejection (the
// reachable-but-erroring condition, distinct from an unreachable server).
func newErrorRegistry(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeFilesystemRegistry writes a minimal one-artifact filesystem registry and
// returns its path.
func writeFilesystemRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "team-shared/company-glossary/ARTIFACT.md", Content: contextArtifactSrc},
	)
	return reg
}

// spec: §7.4 — "podium sync and the SDKs apply the same cache modes."
// offline-only "never contact the registry; structured error if cache miss."
// podium sync keeps no offline content cache, so an offline-only sync against a
// server-source registry has nothing local to materialize and returns the
// structured network.offline_cache_miss error without dialing the server. The
// server URL points at an unbound port, so a dial would surface a transport
// error instead of ErrOfflineCacheMiss (F-7.4.3).
func TestRun_OfflineOnlyServerSourceMisses(t *testing.T) {
	t.Parallel()
	_, err := Run(Options{
		RegistryPath: "http://127.0.0.1:1", // unbound port → connect refused if dialed
		Target:       t.TempDir(),
		AdapterID:    "none",
		CacheMode:    "offline-only",
		HTTPClient:   &http.Client{},
	})
	if !errors.Is(err, ErrOfflineCacheMiss) {
		t.Fatalf("err = %v, want ErrOfflineCacheMiss", err)
	}
}

// spec: §7.4 — offline-only is a no-op for a filesystem source: the registry is
// read locally and is never contacted over the network, so the "cache only"
// contract is trivially satisfied and the sync materializes normally.
func TestRun_OfflineOnlyFilesystemSourceMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeFilesystemRegistry(t)
	target := t.TempDir()
	res, err := Run(Options{
		RegistryPath: reg,
		Target:       target,
		AdapterID:    "none",
		CacheMode:    "offline-only",
	})
	if err != nil {
		t.Fatalf("filesystem offline-only Run: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("offline-only filesystem sync materialized nothing: %+v", res)
	}
}

// spec: §7.4 — offline-first "no error; serve cached results silently." When
// the server-source registry is unreachable, the prior run's materialized
// output stays in place and the sync reports a no-op offline result rather than
// failing. The Result echoes the prior lock so the caller sees the served
// state (F-7.4.3).
func TestRun_OfflineFirstServerUnreachableIsNoop(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"team/glossary": {
			typ:          "context",
			layer:        "team-shared",
			frontmatter:  contextArtifactSrc,
			manifestBody: "Glossary body.\n",
		},
	})
	target := t.TempDir()
	// First sync against the live server populates the target + lock.
	if _, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", HTTPClient: srv.Client(), CacheMode: "always-revalidate"}); err != nil {
		t.Fatalf("warm sync: %v", err)
	}
	artifactPath := filepath.Join(target, "team", "glossary", "ARTIFACT.md")
	if readFileT(t, artifactPath) != contextArtifactSrc {
		t.Fatalf("warm sync did not materialize the artifact")
	}

	// Take the server down (point at an unbound port) and rerun in
	// offline-first mode: no error, existing output untouched, Offline set.
	res, err := Run(Options{
		RegistryPath: "http://127.0.0.1:1",
		Target:       target,
		AdapterID:    "none",
		HTTPClient:   &http.Client{},
		CacheMode:    "offline-first",
	})
	if err != nil {
		t.Fatalf("offline-first unreachable Run errored, want no-op: %v", err)
	}
	if !res.Offline {
		t.Errorf("Result.Offline = false, want true")
	}
	// The previously materialized file must survive the offline-first no-op
	// (no stale-file cleanup runs).
	if readFileT(t, artifactPath) != contextArtifactSrc {
		t.Errorf("offline-first no-op removed or altered the materialized artifact")
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].ID != "team/glossary" {
		t.Errorf("Result.Artifacts = %+v, want the prior lock's team/glossary entry", res.Artifacts)
	}
}

// spec: §7.4 — a structured registry rejection is not the unreachable
// condition, so offline-first must still fail rather than masking it as a
// silent no-op. A server returning a 5xx envelope is reachable.
func TestRun_OfflineFirstStructuredErrorStillFails(t *testing.T) {
	t.Parallel()
	srv := newErrorRegistry(t, http.StatusInternalServerError, `{"code":"registry.unavailable","message":"boom"}`)
	_, err := Run(Options{
		RegistryPath: srv.URL,
		Target:       t.TempDir(),
		AdapterID:    "none",
		HTTPClient:   srv.Client(),
		CacheMode:    "offline-first",
	})
	if err == nil {
		t.Fatal("offline-first masked a structured registry error as a no-op")
	}
	if errors.Is(err, ErrOfflineCacheMiss) {
		t.Errorf("structured error mislabeled as offline cache miss: %v", err)
	}
}
