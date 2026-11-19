package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigFile writes body as registry.yaml in a temporary directory and
// points PODIUM_CONFIG_FILE at it.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	return path
}

// Spec: §13.12 — a registry.yaml that does not decode refuses the start with
// config.invalid naming the file and, when the parser reports one, the line.
// The refusal quotes no other part of the parser's message, so a secret that
// ${ENV_VAR} interpolation placed in the file never reaches it, whether the
// parser would quote it as an anchor name or as a mistyped value.
func TestValidate_UnparseableConfigFileRefusesStart(t *testing.T) {
	const secret = "s3cretTail"
	t.Setenv("PODIUM_TEST_GIT_TOKEN", "*"+secret)
	t.Setenv("PODIUM_TEST_PORT", secret)
	cases := []struct {
		name, body, want string
	}{
		{"syntax error", "registry:\n  bind: 127.0.0.1:0\n  layers: [\n", "does not parse at line "},
		{"type error", "registry:\n  bind: 127.0.0.1:0\n  discovery:\n    notable_count: ${PODIUM_TEST_PORT}\n", "does not parse at line 4"},
		{"anchor from a substituted token", "registry:\n  layers:\n    - id: team\n      source:\n        git:\n          token: ${PODIUM_TEST_GIT_TOKEN}\n", "does not parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigFile(t, tc.body)
			err := LoadConfig().validate()
			if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: registry configuration "+path+" ") ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate = %v, want config.invalid naming %s and %q", err, path, tc.want)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "yaml:") {
				t.Errorf("refusal quotes the parser or the file: %v", err)
			}
		})
	}
}

// Spec: §13.12 — a registry.yaml that exists but cannot be read refuses the
// start with config.invalid naming the file, and `podium config show
// --server` reads the same error through ConfigFileError.
func TestValidate_UnreadableConfigFileRefusesStart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 file")
	}
	path := writeConfigFile(t, "registry:\n  bind: 127.0.0.1:0\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	err := cfg.validate()
	want := "config.invalid: registry configuration " + path + " cannot be read: permission denied"
	if err == nil || err.Error() != want || cfg.ConfigFileError() != err {
		t.Errorf("validate = %v, ConfigFileError = %v, want %q from both", err, cfg.ConfigFileError(), want)
	}
}

// Spec: §13.12 — a registry.yaml that decodes, and an absent one, leave the
// start to the other checks.
func TestValidate_ReadableConfigFileStarts(t *testing.T) {
	writeConfigFile(t, "registry:\n  bind: 127.0.0.1:0\n")
	if cfg := LoadConfig(); cfg.ConfigFileError() != nil {
		t.Errorf("ConfigFileError = %v, want nil", cfg.ConfigFileError())
	}
	t.Setenv("PODIUM_CONFIG_FILE", "")
	t.Setenv("HOME", t.TempDir())
	if cfg := LoadConfig(); cfg.ConfigFileError() != nil {
		t.Errorf("absent file: ConfigFileError = %v, want nil", cfg.ConfigFileError())
	}
}
