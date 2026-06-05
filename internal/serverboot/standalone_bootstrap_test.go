package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §13.10 / §14.3 step 1 / §14.10 step 1 — a
// first-run standalone server writes ~/.podium/sync.yaml pointing at the local
// server, plus ~/.podium/registry.yaml and ~/podium-artifacts/.
func TestBootstrapStandaloneFiles_WritesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "")
	t.Setenv("PODIUM_CONFIG_FILE", "")

	cfg := &Config{
		bind:           "127.0.0.1:8080",
		publicURL:      "http://127.0.0.1:8080",
		storeType:      "sqlite",
		sqlitePath:     filepath.Join(home, ".podium", "standalone", "podium.db"),
		filesystemRoot: filepath.Join(home, ".podium", "standalone", "objects"),
	}
	bootstrapStandaloneFiles(cfg)

	syncBody := mustReadFile(t, filepath.Join(home, ".podium", "sync.yaml"))
	if !strings.Contains(syncBody, "registry: http://127.0.0.1:8080") {
		t.Errorf("sync.yaml missing defaults.registry pointer:\n%s", syncBody)
	}
	regBody := mustReadFile(t, filepath.Join(home, ".podium", "registry.yaml"))
	for _, want := range []string{"bind: 127.0.0.1:8080", "type: sqlite", "type: filesystem"} {
		if !strings.Contains(regBody, want) {
			t.Errorf("registry.yaml missing %q:\n%s", want, regBody)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, "podium-artifacts")); err != nil || !fi.IsDir() {
		t.Errorf("~/podium-artifacts not created: err=%v", err)
	}
}

// spec: §13.10 line 118 — PODIUM_NO_AUTOSTANDALONE suppresses the auto-bootstrap
// (CI, image builds, test isolation).
func TestBootstrapStandaloneFiles_NoAutostandaloneSuppresses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "1")

	bootstrapStandaloneFiles(&Config{storeType: "sqlite", publicURL: "http://127.0.0.1:8080"})

	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); !os.IsNotExist(err) {
		t.Errorf("sync.yaml written despite PODIUM_NO_AUTOSTANDALONE: err=%v", err)
	}
}

// spec: §13.10 line 118 — an explicit --config (PODIUM_CONFIG_FILE) suppresses
// the auto-bootstrap; the operator chose the config.
func TestBootstrapStandaloneFiles_ExplicitConfigSuppresses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(home, "custom.yaml"))

	bootstrapStandaloneFiles(&Config{storeType: "sqlite", publicURL: "http://127.0.0.1:8080"})

	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); !os.IsNotExist(err) {
		t.Errorf("sync.yaml written despite PODIUM_CONFIG_FILE: err=%v", err)
	}
}

// A standard (Postgres) deployment does not auto-write standalone client config.
func TestBootstrapStandaloneFiles_PostgresSkips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "")
	t.Setenv("PODIUM_CONFIG_FILE", "")

	bootstrapStandaloneFiles(&Config{storeType: "postgres", publicURL: "http://127.0.0.1:8080"})

	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); !os.IsNotExist(err) {
		t.Errorf("sync.yaml written for a standard (postgres) deployment: err=%v", err)
	}
}

// An existing sync.yaml is preserved verbatim; the bootstrap never clobbers a
// hand-written client config.
func TestBootstrapStandaloneFiles_PreservesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "")
	t.Setenv("PODIUM_CONFIG_FILE", "")

	dir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = "defaults:\n  registry: http://other.example:9000\n"
	if err := os.WriteFile(filepath.Join(dir, "sync.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	bootstrapStandaloneFiles(&Config{
		bind:      "127.0.0.1:8080",
		publicURL: "http://127.0.0.1:8080",
		storeType: "sqlite",
	})

	if got := mustReadFile(t, filepath.Join(dir, "sync.yaml")); got != existing {
		t.Errorf("existing sync.yaml clobbered:\ngot:  %q\nwant: %q", got, existing)
	}
}

func mustReadFile(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
