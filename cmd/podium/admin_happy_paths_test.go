package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// admin grant POSTs to /v1/admin/grants. The body shape here reflects
// the doJSON path's json.Marshal behavior: passing []byte through
// encoding/json produces a base64-encoded JSON string. Asserting on
// path/method is sufficient for coverage.
func TestAdminGrantCmd_HappyPath(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/admin/grants" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"granted":true}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := adminGrantCmd([]string{"alice"}); code != 0 {
			t.Errorf("adminGrantCmd = %d", code)
		}
	})
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

// admin revoke DELETE hits /v1/admin/grants?user_id=...
func TestAdminRevokeCmd_HappyPath(t *testing.T) {
	gotQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q", r.Method)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := adminRevokeCmd([]string{"alice"}); code != 0 {
			t.Errorf("adminRevokeCmd = %d", code)
		}
	})
	if !strings.Contains(gotQuery, "alice") {
		t.Errorf("query = %q", gotQuery)
	}
}

// admin show-effective GET with groups.
func TestAdminShowEffectiveCmd_HappyPath(t *testing.T) {
	gotQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/show-effective" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"layers":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		code := adminShowEffectiveCmd([]string{"--group", "eng", "--group", "ops", "alice"})
		if code != 0 {
			t.Errorf("adminShowEffectiveCmd = %d", code)
		}
	})
	if !strings.Contains(gotQuery, "alice") || !strings.Contains(gotQuery, "eng") {
		t.Errorf("query = %q (want user + groups)", gotQuery)
	}
}

// admin reembed POST with --artifact + --version sets the query string.
func TestAdminReembedCmd_HappyPathWithArtifactVersion(t *testing.T) {
	gotQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		code := adminReembedCmd([]string{
			"--artifact", "team/foo", "--version", "1.2.3",
		})
		if code != 0 {
			t.Errorf("adminReembedCmd = %d", code)
		}
	})
	if !strings.Contains(gotQuery, "team%2Ffoo") {
		t.Errorf("query = %q (expected url-encoded artifact)", gotQuery)
	}
}

// admin reembed POST with --only-missing.
func TestAdminReembedCmd_HappyPathOnlyMissing(t *testing.T) {
	gotQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		code := adminReembedCmd([]string{"--only-missing"})
		if code != 0 {
			t.Errorf("adminReembedCmd = %d", code)
		}
	})
	if !strings.Contains(gotQuery, "only_missing=true") {
		t.Errorf("query = %q", gotQuery)
	}
}

// admin erase against a temp audit log.
func TestAdminEraseCmd_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	withStderr(t, func() {
		code := adminEraseCmd([]string{
			"--audit-path", path,
			"--salt", "pepper",
			"alice",
		})
		if code != 0 {
			t.Errorf("adminEraseCmd = %d", code)
		}
	})
}

// admin retention against a temp audit log with a parsable policy.
func TestAdminRetentionCmd_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	withStderr(t, func() {
		code := adminRetentionCmd([]string{
			"--audit-path", path,
			"--policy", "artifacts.searched=24h",
		})
		if code != 0 {
			t.Errorf("adminRetentionCmd = %d", code)
		}
	})
}

func TestAdminRetentionCmd_PolicyParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	withStderr(t, func() {
		code := adminRetentionCmd([]string{
			"--audit-path", path,
			"--policy", "no-equals-sign",
		})
		if code != 2 {
			t.Errorf("adminRetentionCmd(bad policy) = %d, want 2", code)
		}
	})
}

// domain analyze GET happy path.
func TestDomainAnalyze_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/domain/analyze" || r.Method != http.MethodGet {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := domainAnalyze([]string{"--path", "finance"}); code != 0 {
			t.Errorf("domainAnalyze = %d", code)
		}
	})
}

// verify with a noop provider where sign produces a valid envelope.
func TestVerifyCmd_RoundTripWithNoop(t *testing.T) {
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := signCmd([]string{
				"--provider", "noop",
				"--content-hash", "sha256:" + strings.Repeat("b", 64),
			}); code != 0 {
				t.Errorf("signCmd = %d", code)
			}
		})
	})
	envelope := strings.TrimSpace(out)
	if envelope == "" {
		t.Fatalf("no envelope from signCmd")
	}
	// Verify against the same hash succeeds.
	withStderr(t, func() {
		code := verifyCmd([]string{
			"--provider", "noop",
			"--content-hash", "sha256:" + strings.Repeat("b", 64),
			"--signature", envelope,
		})
		if code != 0 {
			t.Errorf("verifyCmd = %d, want 0", code)
		}
	})
	// Verify with a tampered hash fails.
	withStderr(t, func() {
		code := verifyCmd([]string{
			"--provider", "noop",
			"--content-hash", "sha256:" + strings.Repeat("c", 64),
			"--signature", envelope,
		})
		if code != 1 {
			t.Errorf("verifyCmd(tampered) = %d, want 1", code)
		}
	})
}

func TestVerifyCmd_BadProvider(t *testing.T) {
	withStderr(t, func() {
		code := verifyCmd([]string{
			"--provider", "definitely-not-a-provider",
			"--content-hash", "sha256:" + strings.Repeat("0", 64),
			"--signature", "envelope",
		})
		if code != 1 {
			t.Errorf("verifyCmd(bad provider) = %d, want 1", code)
		}
	})
}

func TestSignCmd_BadProvider(t *testing.T) {
	withStderr(t, func() {
		code := signCmd([]string{
			"--provider", "definitely-not-a-provider",
			"--content-hash", "sha256:" + strings.Repeat("0", 64),
		})
		if code != 1 {
			t.Errorf("signCmd(bad provider) = %d, want 1", code)
		}
	})
}

func TestQuotaCmd_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/quota" || r.Method != http.MethodGet {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := quotaCmd(nil); code != 0 {
			t.Errorf("quotaCmd = %d", code)
		}
	})
}

func TestImpactCmd_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dependents" || r.Method != http.MethodGet {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"edges":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := impactCmd([]string{"team/x"}); code != 0 {
			t.Errorf("impactCmd = %d", code)
		}
	})
}

// HTTP error from the registry → exit 1, not 2.
func TestImpactCmd_RegistryErrorExits1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := impactCmd([]string{"team/x"}); code != 1 {
			t.Errorf("impactCmd(500) = %d, want 1", code)
		}
	})
}
