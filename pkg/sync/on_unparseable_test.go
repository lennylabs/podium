package sync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// envOf returns a getenv that reads PODIUM_SYNC_ON_UNPARSEABLE as v and every
// other variable as unset.
func envOf(v string) func(string) string {
	return func(k string) string {
		if k == sync.OnUnparseableEnv {
			return v
		}
		return ""
	}
}

// Spec: §7.5.2, §13.11.3 — the setting resolves from --on-unparseable, then
// PODIUM_SYNC_ON_UNPARSEABLE, then defaults.on_unparseable in the merged
// sync.yaml, then keep. An empty value at any step is unset.
func TestResolveOnUnparseable_Precedence(t *testing.T) {
	t.Parallel()
	merged := func(v string) *sync.MergedConfig {
		return &sync.MergedConfig{Defaults: sync.Defaults{OnUnparseable: v}}
	}
	cases := []struct {
		name   string
		flag   string
		env    string
		merged *sync.MergedConfig
		want   sync.OnUnparseable
	}{
		{"nothing set", "", "", nil, sync.OnUnparseableKeep},
		{"empty merged config", "", "", merged(""), sync.OnUnparseableKeep},
		{"sync.yaml", "", "", merged("remove"), sync.OnUnparseableRemove},
		{"env over sync.yaml", "", "keep", merged("remove"), sync.OnUnparseableKeep},
		{"flag over env", "remove", "keep", merged("keep"), sync.OnUnparseableRemove},
		{"flag over sync.yaml", "keep", "", merged("remove"), sync.OnUnparseableKeep},
	}
	for _, tc := range cases {
		got, err := sync.ResolveOnUnparseable(tc.flag, envOf(tc.env), tc.merged)
		if err != nil || got != tc.want {
			t.Errorf("%s: ResolveOnUnparseable = (%q, %v), want (%q, nil)", tc.name, got, err, tc.want)
		}
	}
}

// Spec: §7.5.2, §13.11.3 — a value other than keep or remove is an error that
// names its source and the accepted values, at every step, and a higher step
// that holds a valid value hides an invalid lower one.
func TestResolveOnUnparseable_InvalidValue(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{Defaults: sync.Defaults{OnUnparseable: "discard"}}
	cases := []struct {
		name, flag, env string
		merged          *sync.MergedConfig
		source          string
	}{
		{"flag", "Keep", "", nil, "--on-unparseable"},
		{"env", "", "drop", nil, sync.OnUnparseableEnv},
		{"sync.yaml", "", "", merged, "defaults.on_unparseable"},
	}
	for _, tc := range cases {
		_, err := sync.ResolveOnUnparseable(tc.flag, envOf(tc.env), tc.merged)
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if msg := err.Error(); !strings.HasPrefix(msg, tc.source+" must be keep | remove, got ") {
			t.Errorf("%s: error = %q, want it to name %s and keep | remove", tc.name, msg, tc.source)
		}
	}
	if got, err := sync.ResolveOnUnparseable("remove", envOf(""), merged); err != nil || got != sync.OnUnparseableRemove {
		t.Errorf("valid flag over an invalid sync.yaml = (%q, %v), want (remove, nil)", got, err)
	}
}

// Spec: §7.5.2, §13.11.3 — defaults.on_unparseable resolves across the three
// file scopes by per-key precedence: project-local over project-shared over
// user-global.
func TestLoadMergedConfig_OnUnparseableScopes(t *testing.T) {
	t.Parallel()
	ws, home := listingWorkspace(t)
	if err := os.MkdirAll(filepath.Join(home, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePodiumFile(t, home, "sync.yaml", "defaults:\n  on_unparseable: remove\n")
	steps := []struct {
		file, content string
		want          string
	}{
		{"", "", "remove"},
		{"sync.yaml", "defaults:\n  on_unparseable: keep\n", "keep"},
		{"sync.local.yaml", "defaults:\n  on_unparseable: remove\n", "remove"},
	}
	for _, st := range steps {
		if st.file != "" {
			writePodiumFile(t, ws, st.file, st.content)
		}
		merged, _, err := sync.LoadMergedConfig(ws, home)
		if err != nil {
			t.Fatalf("LoadMergedConfig: %v", err)
		}
		if got := merged.Defaults.OnUnparseable; got != st.want {
			t.Errorf("after %q: defaults.on_unparseable = %q, want %q", st.file, got, st.want)
		}
	}
}

// Spec: §13.11.3 — Run and Override refuse a value other than keep or remove
// from a library caller that skipped ResolveOnUnparseable. Override refuses it
// before it records a toggle.
func TestRunAndOverride_RefuseInvalidOnUnparseable(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	target := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: reg, Target: target, OnUnparseable: "discard"}); err == nil ||
		!strings.Contains(err.Error(), "on_unparseable must be keep | remove") {
		t.Errorf("Run error = %v, want the on_unparseable refusal", err)
	}
	_, err := sync.Override(sync.OverrideOptions{Target: target, Add: []string{"x/y"}, OnUnparseable: "discard"})
	if err == nil || !strings.Contains(err.Error(), "on_unparseable must be keep | remove") {
		t.Errorf("Override error = %v, want the on_unparseable refusal", err)
	}
	if _, err := os.Stat(sync.LockFilePath(target)); !os.IsNotExist(err) {
		t.Errorf("Override wrote a lock before refusing: stat err = %v", err)
	}
}
