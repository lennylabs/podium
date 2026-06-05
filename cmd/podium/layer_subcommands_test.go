package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	// Isolate HOME and the working directory so the sync.yaml fallback
	// (register/reingest) finds no merged defaults.registry and the commands
	// still refuse with exit 2.
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
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

// spec §7.3.1 / §14.10: --interval takes a Go-style duration, so
// the documented `--interval 1h` parses. A bare integer (the old
// seconds-only form) is rejected by flag parsing with exit 2.
func TestLayerWatch_IntervalIsDuration(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")

	// A bare integer is no longer a valid interval.
	withStderr(t, func() {
		if code := layerWatch([]string{"--id", "x", "--interval", "3600"}); code != 2 {
			t.Errorf("layerWatch(--interval 3600) = %d, want 2 (bare int rejected)", code)
		}
	})

	// The §14.10 duration example parses; stub the sleep so the watch loop
	// exits after the first poke instead of blocking.
	orig := sleepFor
	t.Cleanup(func() { sleepFor = orig })
	var gotInterval time.Duration
	sleepFor = func(d time.Duration) { gotInterval = d; runtimeGoexit() }
	done := make(chan struct{})
	go func() {
		defer close(done)
		withStderr(t, func() {
			captureStdout(t, func() {
				_ = layerWatch([]string{"--id", "x", "--registry", "http://127.0.0.1:1", "--interval", "1h"})
			})
		})
	}()
	<-done
	if gotInterval != time.Hour {
		t.Errorf("parsed interval = %v, want 1h", gotInterval)
	}
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

// --- standalone sync.yaml registry fallback ---------------------

// spec §14.10: an explicit --registry / PODIUM_REGISTRY value
// always wins; resolveLayerRegistry returns it unchanged.
func TestResolveLayerRegistry_FlagValueWins(t *testing.T) {
	t.Parallel()
	if got := resolveLayerRegistry("http://flag.example"); got != "http://flag.example" {
		t.Errorf("resolveLayerRegistry(flag) = %q, want the flag value", got)
	}
}

// spec §14.10: with no flag/env registry, the layer commands fall
// back to defaults.registry in the merged ~/.podium/sync.yaml that
// `podium serve --standalone` bootstraps.
func TestMergedRegistry_ReadsHomeSyncYAML(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	podiumDir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(podiumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("defaults:\n  registry: http://127.0.0.1:8088\n")
	if err := os.WriteFile(filepath.Join(podiumDir, "sync.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// A clean workspace dir (no .podium) so only the home scope contributes.
	if got := mergedRegistry(t.TempDir(), home); got != "http://127.0.0.1:8088" {
		t.Errorf("mergedRegistry = %q, want the bootstrapped registry", got)
	}
}

// spec §14.10: with no sync.yaml anywhere, mergedRegistry resolves
// to the empty string so the command refuses with the missing-registry error.
func TestMergedRegistry_EmptyWhenNoConfig(t *testing.T) {
	t.Parallel()
	if got := mergedRegistry(t.TempDir(), t.TempDir()); got != "" {
		t.Errorf("mergedRegistry(no config) = %q, want empty", got)
	}
}

// spec §14.10: `podium layer register` with no --registry resolves
// the registry from the bootstrapped ~/.podium/sync.yaml and reaches the
// server, end to end through the command.
func TestLayerRegister_FallsBackToSyncYAMLRegistry(t *testing.T) {
	// Isolate HOME and the working directory so only the sync.yaml we write
	// contributes; empty PODIUM_REGISTRY forces the fallback path.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_REGISTRY", "")
	t.Chdir(t.TempDir())

	gotPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"community-skills","registered":true}`))
	}))
	t.Cleanup(srv.Close)

	podiumDir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(podiumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("defaults:\n  registry: " + srv.URL + "\n")
	if err := os.WriteFile(filepath.Join(podiumDir, "sync.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	withStderr(t, func() {
		captureStdout(t, func() {
			if code := layerRegister([]string{"--id", "community-skills", "--local", t.TempDir()}); code != 0 {
				t.Fatalf("layerRegister (sync.yaml fallback) = %d, want 0", code)
			}
		})
	})
	if gotPath != "/v1/layers" {
		t.Errorf("registry not reached via fallback; server saw path %q", gotPath)
	}
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
