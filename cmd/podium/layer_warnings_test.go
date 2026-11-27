package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// unappliedVisibilityRegistry serves a layer endpoint wired the way a
// registry with no request-time identity verifier is, so its write responses
// carry the §7.3.1 unapplied-visibility warning.
func unappliedVisibilityRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "default", server.NewModeTracker()).WithUnappliedVisibility()
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §7.3.1 (Unapplied-visibility warning) — `podium layer register`,
// `podium layer update`, and `podium layer restore` write the response's
// warnings to standard error, one per line prefixed "warning: ", and keep the
// response body on standard output.
func TestLayerCommands_WriteVisibilityWarningsToStderr(t *testing.T) {
	ts := unappliedVisibilityRegistry(t)
	want := `warning: layer "alice-personal" declares visibility users: ["alice@acme.com"], but this registry verifies no caller and applies no layer visibility, so every caller sees the layer and can update or unregister it`

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if rc := layerRegister([]string{
				"--registry", ts.URL, "--id", "alice-personal", "--local", t.TempDir(),
				"--user-defined", "--owner", "alice@acme.com",
			}); rc != 0 {
				t.Errorf("layerRegister rc = %d, want 0", rc)
			}
		})
	})
	if !strings.Contains(stderr, want) {
		t.Errorf("register stderr = %q, want it to contain %q", stderr, want)
	}
	if strings.Contains(stdout, "warning: ") || !strings.Contains(stdout, `"warnings"`) {
		t.Errorf("register stdout = %q, want the response body alone", stdout)
	}

	stderr = captureStderr(t, func() {
		_ = captureStdout(t, func() {
			if rc := layerUpdate([]string{"--registry", ts.URL, "--id", "alice-personal", "--ref", "main"}); rc != 0 {
				t.Errorf("layerUpdate rc = %d, want 0", rc)
			}
		})
	})
	if !strings.Contains(stderr, want) {
		t.Errorf("update stderr = %q, want it to contain %q", stderr, want)
	}

	withStderr(t, func() {
		_ = captureStdout(t, func() {
			if rc := layerUnregister([]string{"--registry", ts.URL, "alice-personal"}); rc != 0 {
				t.Errorf("layerUnregister rc = %d, want 0", rc)
			}
		})
	})
	stderr = captureStderr(t, func() {
		_ = captureStdout(t, func() {
			if rc := layerRestore([]string{"--registry", ts.URL, "alice-personal"}); rc != 0 {
				t.Errorf("layerRestore rc = %d, want 0", rc)
			}
		})
	})
	if !strings.Contains(stderr, want) {
		t.Errorf("restore stderr = %q, want it to contain %q", stderr, want)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — a public layer's
// registration carries no warning, so the CLI writes none.
func TestLayerRegister_PublicLayerWritesNoWarning(t *testing.T) {
	ts := unappliedVisibilityRegistry(t)
	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() {
			if rc := layerRegister([]string{"--registry", ts.URL, "--id", "open", "--local", t.TempDir(), "--public"}); rc != 0 {
				t.Errorf("layerRegister rc = %d, want 0", rc)
			}
		})
	})
	if strings.Contains(stderr, "warning: ") {
		t.Errorf("stderr = %q, want no warning for a public layer", stderr)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — writeResponseWarnings writes
// one prefixed line per warning and nothing for a body without warnings or a
// body that is not a JSON object.
func TestWriteResponseWarnings(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"two warnings", `{"warnings":["first","second"]}`, "warning: first\nwarning: second\n"},
		{"no warnings member", `{"restored":"x"}`, ""},
		{"not a JSON object", `queued`, ""},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		writeResponseWarnings(&buf, []byte(tc.body))
		if buf.String() != tc.want {
			t.Errorf("%s: wrote %q, want %q", tc.name, buf.String(), tc.want)
		}
	}
}
