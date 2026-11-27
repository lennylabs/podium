package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.12 — PODIUM_GIT_FETCH_DEPTH resolves from the environment, then
// the registry.yaml git_fetch_depth key, then the default of 16, and a set
// value that is not an integer from 1 to 2147483646 carries a config.invalid
// refusal that names the setting it came from. 2147483647, which Git reads as
// a request for the complete history, is refused.
func TestResolveGitFetchDepth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, env, file string
		want            int
		wantSource      string
		wantErr         string
	}{
		{name: "default", want: 16, wantSource: "default"},
		{name: "env", env: "64", want: 64, wantSource: "PODIUM_GIT_FETCH_DEPTH"},
		{name: "env one", env: "1", want: 1, wantSource: "PODIUM_GIT_FETCH_DEPTH"},
		{name: "env largest", env: "2147483646", want: 2147483646, wantSource: "PODIUM_GIT_FETCH_DEPTH"},
		{name: "file", file: " 32 ", want: 32, wantSource: "registry.yaml"},
		{name: "env beats file", env: "8", file: "32", want: 8, wantSource: "PODIUM_GIT_FETCH_DEPTH"},
		{name: "blank env falls back to file", env: "  ", file: "32", want: 32, wantSource: "registry.yaml"},
		{name: "env shadows an invalid file value", env: "8", file: "deep", want: 8, wantSource: "PODIUM_GIT_FETCH_DEPTH"},
		{name: "env zero", env: "0", wantErr: `config.invalid: PODIUM_GIT_FETCH_DEPTH must be an integer from 1 to 2147483646, such as 16, got "0"`},
		{name: "env negative", env: "-1", wantErr: "config.invalid: PODIUM_GIT_FETCH_DEPTH"},
		{name: "env complete history", env: "2147483647", wantErr: "config.invalid: PODIUM_GIT_FETCH_DEPTH"},
		{name: "env beyond int32", env: "9999999999", wantErr: "config.invalid: PODIUM_GIT_FETCH_DEPTH"},
		{name: "env not an integer", env: "16.5", wantErr: "config.invalid: PODIUM_GIT_FETCH_DEPTH"},
		{name: "file not an integer", file: "deep", wantErr: "config.invalid: registry.yaml git_fetch_depth must be an integer from 1 to 2147483646"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveGitFetchDepth(tc.env, tc.file)
			if tc.wantErr != "" {
				if got.err == nil || !strings.HasPrefix(got.err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want prefix %q", got.err, tc.wantErr)
				}
				return
			}
			if got.err != nil || got.depth != tc.want || got.source != tc.wantSource {
				t.Errorf("got %d from %q (err %v), want %d from %q", got.depth, got.source, got.err, tc.want, tc.wantSource)
			}
		})
	}
}

// Spec: §13.12 — LoadConfig reads git_fetch_depth from registry.yaml when the
// variable is unset, `podium config show` reports the value and its source,
// and validate refuses a value outside 1 to 2147483646 with config.invalid.
func TestLoadConfig_GitFetchDepth(t *testing.T) {
	write := func(t *testing.T, body string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "registry.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Setenv("PODIUM_CONFIG_FILE", path)
	}
	t.Run("file value", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_depth: 48\n")
		t.Setenv("PODIUM_GIT_FETCH_DEPTH", "")
		c := LoadConfig()
		if c.gitFetchDepth.depth != 48 || c.gitFetchDepth.err != nil {
			t.Fatalf("gitFetchDepth = %+v, want 48 and no error", c.gitFetchDepth)
		}
		if !hasSetting(c.Settings(), Setting{"git_fetch_depth", "48", "registry.yaml"}) {
			t.Errorf("Settings() does not report git_fetch_depth=48 from registry.yaml: %v", c.Settings())
		}
		if errs := c.SettingErrors(); errs != nil {
			t.Errorf("SettingErrors() = %v, want none", errs)
		}
	})
	t.Run("default", func(t *testing.T) {
		write(t, "registry: {}\n")
		t.Setenv("PODIUM_GIT_FETCH_DEPTH", "")
		if !hasSetting(LoadConfig().Settings(), Setting{"git_fetch_depth", "16", "default"}) {
			t.Errorf("Settings() does not report the default git_fetch_depth=16")
		}
	})
	t.Run("file value refused", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_depth: 0\n")
		t.Setenv("PODIUM_GIT_FETCH_DEPTH", "")
		err := LoadConfig().validate()
		if err == nil || !strings.Contains(err.Error(), "config.invalid: registry.yaml git_fetch_depth") {
			t.Fatalf("validate() = %v, want config.invalid naming git_fetch_depth", err)
		}
	})
	t.Run("env value refused", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_depth: 48\n")
		t.Setenv("PODIUM_GIT_FETCH_DEPTH", "2147483647")
		c := LoadConfig()
		err := c.validate()
		if err == nil || !strings.Contains(err.Error(), "config.invalid: PODIUM_GIT_FETCH_DEPTH") {
			t.Fatalf("validate() = %v, want config.invalid naming PODIUM_GIT_FETCH_DEPTH", err)
		}
		if !hasSetting(c.Settings(), Setting{"git_fetch_depth", "2147483647 (invalid)", "PODIUM_GIT_FETCH_DEPTH"}) {
			t.Errorf("Settings() does not report git_fetch_depth=2147483647 (invalid): %v", c.Settings())
		}
		if errs := c.SettingErrors(); len(errs) != 1 || errs[0].Error() != err.Error() {
			t.Errorf("SettingErrors() = %v, want [%v]", errs, err)
		}
	})
	t.Run("both settings invalid", func(t *testing.T) {
		write(t, "registry: {}\n")
		t.Setenv("PODIUM_GIT_FETCH_DEPTH", "0")
		t.Setenv("PODIUM_GIT_FETCH_TIMEOUT", "0")
		if errs := LoadConfig().SettingErrors(); len(errs) != 2 {
			t.Errorf("SettingErrors() = %v, want the timeout and the depth refusals", errs)
		}
	})
}
