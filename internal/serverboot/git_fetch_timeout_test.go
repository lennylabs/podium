package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec: §13.12 — PODIUM_GIT_FETCH_TIMEOUT resolves from the environment, then
// the registry.yaml git_fetch_timeout key, then the 10-minute default, and a
// set value that is not a duration above zero carries a config.invalid
// refusal that names the setting it came from.
func TestResolveGitFetchTimeout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, env, file string
		want            time.Duration
		wantSource      string
		wantErr         string
	}{
		{name: "default", want: 10 * time.Minute, wantSource: "default"},
		{name: "env", env: "90s", want: 90 * time.Second, wantSource: "PODIUM_GIT_FETCH_TIMEOUT"},
		{name: "file", file: " 2m ", want: 2 * time.Minute, wantSource: "registry.yaml"},
		{name: "env beats file", env: "30s", file: "2m", want: 30 * time.Second, wantSource: "PODIUM_GIT_FETCH_TIMEOUT"},
		{name: "blank env falls back to file", env: "  ", file: "2m", want: 2 * time.Minute, wantSource: "registry.yaml"},
		// The effective value alone is validated, as for the other settings
		// that a variable and a registry.yaml key both set.
		{name: "env shadows an invalid file value", env: "30s", file: "ten minutes", want: 30 * time.Second, wantSource: "PODIUM_GIT_FETCH_TIMEOUT"},
		{name: "env zero", env: "0s", wantErr: `config.invalid: PODIUM_GIT_FETCH_TIMEOUT must be a duration above zero, such as 10m, got "0s"`},
		{name: "env negative", env: "-1m", wantErr: "config.invalid: PODIUM_GIT_FETCH_TIMEOUT"},
		{name: "env not a duration", env: "600", wantErr: "config.invalid: PODIUM_GIT_FETCH_TIMEOUT"},
		{name: "file not a duration", file: "ten minutes", wantErr: "config.invalid: registry.yaml git_fetch_timeout must be a duration above zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveGitFetchTimeout(tc.env, tc.file)
			if tc.wantErr != "" {
				if got.err == nil || !strings.HasPrefix(got.err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want prefix %q", got.err, tc.wantErr)
				}
				return
			}
			if got.err != nil || got.timeout != tc.want || got.source != tc.wantSource {
				t.Errorf("got %v from %q (err %v), want %v from %q", got.timeout, got.source, got.err, tc.want, tc.wantSource)
			}
		})
	}
}

// Spec: §13.12 — LoadConfig reads git_fetch_timeout from registry.yaml when
// the variable is unset, `podium config show` reports the value and its
// source, and validate refuses a value that is not a duration above zero with
// config.invalid.
func TestLoadConfig_GitFetchTimeout(t *testing.T) {
	write := func(t *testing.T, body string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "registry.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Setenv("PODIUM_CONFIG_FILE", path)
	}
	t.Run("file value", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_timeout: 45s\n")
		t.Setenv("PODIUM_GIT_FETCH_TIMEOUT", "")
		c := LoadConfig()
		if c.gitFetch.timeout != 45*time.Second || c.gitFetch.err != nil {
			t.Fatalf("gitFetch = %+v, want 45s and no error", c.gitFetch)
		}
		if !hasSetting(c.Settings(), Setting{"git_fetch_timeout", "45s", "registry.yaml"}) {
			t.Errorf("Settings() does not report git_fetch_timeout=45s from registry.yaml: %v", c.Settings())
		}
	})
	t.Run("file value refused", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_timeout: 600\n")
		t.Setenv("PODIUM_GIT_FETCH_TIMEOUT", "")
		err := LoadConfig().validate()
		if err == nil || !strings.Contains(err.Error(), "config.invalid: registry.yaml git_fetch_timeout") {
			t.Fatalf("validate() = %v, want config.invalid naming git_fetch_timeout", err)
		}
	})
	t.Run("env value refused", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_timeout: 45s\n")
		t.Setenv("PODIUM_GIT_FETCH_TIMEOUT", "0")
		c := LoadConfig()
		err := c.validate()
		if err == nil || !strings.Contains(err.Error(), "config.invalid: PODIUM_GIT_FETCH_TIMEOUT") {
			t.Fatalf("validate() = %v, want config.invalid naming PODIUM_GIT_FETCH_TIMEOUT", err)
		}
		// `podium config show` reports the value as set, marked invalid, and
		// the refusal validate returns.
		if !hasSetting(c.Settings(), Setting{"git_fetch_timeout", "0 (invalid)", "PODIUM_GIT_FETCH_TIMEOUT"}) {
			t.Errorf("Settings() does not report git_fetch_timeout=0 (invalid): %v", c.Settings())
		}
		if errs := c.SettingErrors(); len(errs) != 1 || errs[0].Error() != err.Error() {
			t.Errorf("SettingErrors() = %v, want [%v]", errs, err)
		}
	})
	t.Run("valid value reports no setting error", func(t *testing.T) {
		write(t, "registry:\n  git_fetch_timeout: 45s\n")
		t.Setenv("PODIUM_GIT_FETCH_TIMEOUT", "")
		if errs := LoadConfig().SettingErrors(); errs != nil {
			t.Errorf("SettingErrors() = %v, want none", errs)
		}
	})
}

// hasSetting reports whether settings holds want.
func hasSetting(settings []Setting, want Setting) bool {
	for _, s := range settings {
		if s == want {
			return true
		}
	}
	return false
}
