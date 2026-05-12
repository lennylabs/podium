package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --- --help and validation paths -------------------------------------------

func TestLayerSubcommands_HelpExitsZero(t *testing.T) {
	t.Parallel()
	for name, cmd := range map[string]func([]string) int{
		"layerRegister":   layerRegister,
		"layerList":       layerList,
		"layerReorder":    layerReorder,
		"layerUnregister": layerUnregister,
		"layerReingest":   layerReingest,
		"layerWatch":      layerWatch,
	} {
		t.Run(name, func(t *testing.T) {
			withStderr(t, func() {
				if code := cmd([]string{"--help"}); code != 0 {
					t.Errorf("%s(--help) = %d, want 0", name, code)
				}
			})
		})
	}
}

func TestLayerSubcommands_MissingRegistryExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	for name, args := range map[string][]string{
		"layerList":       nil,
		"layerRegister":   {"--id", "x", "--local", "/tmp"},
		"layerUnregister": {"x"},
		"layerReingest":   {"x"},
	} {
		t.Run(name, func(t *testing.T) {
			cmds := map[string]func([]string) int{
				"layerList":       layerList,
				"layerRegister":   layerRegister,
				"layerUnregister": layerUnregister,
				"layerReingest":   layerReingest,
			}
			withStderr(t, func() {
				if code := cmds[name](args); code != 2 {
					t.Errorf("%s = %d, want 2", name, code)
				}
			})
		})
	}
}

func TestLayerRegister_NeedsSourceOrLocal(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerRegister([]string{"--id", "x"}); code != 2 {
			t.Errorf("layerRegister(no source) = %d, want 2", code)
		}
	})
}

func TestLayerRegister_NeedsID(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerRegister(nil); code != 2 {
			t.Errorf("layerRegister(no id) = %d, want 2", code)
		}
	})
}

func TestLayerReorder_NoArgsExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerReorder(nil); code != 2 {
			t.Errorf("layerReorder(nil) = %d, want 2", code)
		}
	})
}

func TestLayerUnregister_NoArgsExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerUnregister(nil); code != 2 {
			t.Errorf("layerUnregister(nil) = %d, want 2", code)
		}
	})
}

func TestLayerReingest_NoArgsExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerReingest(nil); code != 2 {
			t.Errorf("layerReingest(nil) = %d, want 2", code)
		}
	})
}

func TestLayerWatch_BadIntervalExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerWatch([]string{"--id", "x", "--interval", "0"}); code != 2 {
			t.Errorf("layerWatch(bad interval) = %d, want 2", code)
		}
	})
}

func TestLayerWatch_MissingIDExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := layerWatch(nil); code != 2 {
			t.Errorf("layerWatch(nil) = %d, want 2", code)
		}
	})
}

// --- Happy paths driven by a stub registry ---------------------------------

// stubRegistry returns 200 with an empty JSON body for every request.
func stubRegistry(t *testing.T, expectMethod string, expectPath string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectMethod != "" && r.Method != expectMethod {
			t.Errorf("method = %q, want %q", r.Method, expectMethod)
		}
		if expectPath != "" && r.URL.Path != expectPath {
			t.Errorf("path = %q, want %q", r.URL.Path, expectPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLayerList_HappyPathHitsGetLayers(t *testing.T) {
	srv := stubRegistry(t, http.MethodGet, "/v1/layers")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := layerList(nil); code != 0 {
				t.Errorf("layerList = %d, want 0", code)
			}
		})
	})
	if out == "" {
		t.Errorf("no stdout output")
	}
}

func TestLayerRegister_HappyPathPostsBody(t *testing.T) {
	gotBody := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/layers" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var buf []byte
		buf = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x","registered":true}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := layerRegister([]string{
			"--id", "team-shared", "--repo", "https://github.com/x/y",
			"--ref", "main", "--root", "artifacts/",
			"--organization",
			"--group", "engineering",
		}); code != 0 {
			t.Errorf("layerRegister = %d, want 0", code)
		}
	})
	if gotBody == "" {
		t.Errorf("did not POST a body")
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(gotBody), &body)
	if body["id"] != "team-shared" {
		t.Errorf("body id = %v", body["id"])
	}
}

func TestLayerReorder_HappyPathPostsOrder(t *testing.T) {
	srv := stubRegistry(t, http.MethodPost, "/v1/layers/reorder")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := layerReorder([]string{"a", "b", "c"}); code != 0 {
			t.Errorf("layerReorder = %d, want 0", code)
		}
	})
}

func TestLayerUnregister_HappyPathDeletes(t *testing.T) {
	srv := stubRegistry(t, http.MethodDelete, "/v1/layers")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := layerUnregister([]string{"x"}); code != 0 {
			t.Errorf("layerUnregister = %d, want 0", code)
		}
	})
}

func TestLayerReingest_HappyPathPosts(t *testing.T) {
	srv := stubRegistry(t, http.MethodPost, "/v1/layers/reingest")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := layerReingest([]string{"x"}); code != 0 {
			t.Errorf("layerReingest = %d, want 0", code)
		}
	})
}

// adminRuntime list/register also have validation paths.

func TestAdminRuntimeRegister_MissingFlagsExits2(t *testing.T) {
	withStderr(t, func() {
		if code := adminRuntimeRegister(nil); code != 2 {
			t.Errorf("adminRuntimeRegister(nil) = %d, want 2", code)
		}
	})
}

func TestAdminRuntimeRegister_MissingKeyFile(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		args := []string{
			"--issuer", "podium-runtime",
			"--algorithm", "RS256",
			"--public-key-file", filepath.Join(t.TempDir(), "absent.pem"),
		}
		if code := adminRuntimeRegister(args); code != 1 {
			t.Errorf("adminRuntimeRegister(missing keyfile) = %d, want 1", code)
		}
	})
}

func TestAdminRuntimeRegister_HappyPath(t *testing.T) {
	srv := stubRegistry(t, http.MethodPost, "/v1/admin/runtime")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	pem := filepath.Join(t.TempDir(), "key.pem")
	_ = os.WriteFile(pem, []byte("-----BEGIN PUBLIC KEY-----\nfake\n-----END PUBLIC KEY-----\n"), 0o644)
	withStderr(t, func() {
		args := []string{
			"--issuer", "podium-runtime",
			"--algorithm", "RS256",
			"--public-key-file", pem,
		}
		if code := adminRuntimeRegister(args); code != 0 {
			t.Errorf("adminRuntimeRegister happy = %d, want 0", code)
		}
	})
}

func TestAdminRuntimeList_HappyPath(t *testing.T) {
	srv := stubRegistry(t, http.MethodGet, "/v1/admin/runtime")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if code := adminRuntimeList(nil); code != 0 {
			t.Errorf("adminRuntimeList = %d, want 0", code)
		}
	})
}
