package serverboot

import (
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.12, §4.7.9 — a registry with signing on and no
// PODIUM_SIGN_KEY_PATH refuses to start unless its store is the SQLite store
// in the directory the default key resolves to. A memory store strands no
// signature, and signing off or a set key path needs no refusal. The error
// names the backend, PODIUM_SIGN_KEY_PATH, and PODIUM_SIGN=none, and carries
// no DSN.
func TestRefuseUnpersistedSigningKey(t *testing.T) {
	home := t.TempDir()
	defaultDB := filepath.Join(home, ".podium", "standalone", "podium.db")
	otherDB := filepath.Join(home, "elsewhere", "podium.db")
	const dsn = "postgres://podium:hunter2@db.acme.com/podium"
	cases := []struct {
		name    string
		cfg     Config
		keyPath string
		refuse  bool
		want    []string
	}{
		{name: "signing off", cfg: Config{signMode: "none", storeType: "postgres", postgresDSN: dsn}},
		{name: "key path set", cfg: Config{storeType: "postgres", postgresDSN: dsn}, keyPath: "/srv/podium/registry-signing.key"},
		{name: "memory store", cfg: Config{storeType: "memory"}},
		{name: "sqlite in the default directory", cfg: Config{storeType: "sqlite", sqlitePath: defaultDB}},
		{name: "sqlite in the default directory, unclean path", cfg: Config{signMode: "registry-key", storeType: "sqlite", sqlitePath: home + "/.podium/standalone/./podium.db"}},
		{
			name:   "sqlite in another directory",
			cfg:    Config{storeType: "sqlite", sqlitePath: otherDB},
			refuse: true,
			want:   []string{"sqlite", otherDB, "PODIUM_SIGN_KEY_PATH", "PODIUM_SIGN=none"},
		},
		{
			name:   "postgres store",
			cfg:    Config{signMode: "registry-key", storeType: "postgres", postgresDSN: dsn},
			refuse: true,
			want:   []string{"postgres", "PODIUM_SIGN_KEY_PATH", "PODIUM_SIGN=none"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("PODIUM_SIGN_KEY_PATH", tc.keyPath)
			err := refuseUnpersistedSigningKey(&tc.cfg)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("refuseUnpersistedSigningKey = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("refuseUnpersistedSigningKey = nil, want a refusal")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q does not name %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "db.acme.com") {
				t.Errorf("refusal %q carries the DSN", err)
			}
		})
	}
}

// Spec: §13.12 — a home os.UserHomeDir cannot resolve leaves the default key
// path unresolvable, so the start is refused rather than admitted.
func TestRefuseUnpersistedSigningKey_UnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	cfg := Config{storeType: "sqlite", sqlitePath: "/var/lib/podium/podium.db"}
	if err := refuseUnpersistedSigningKey(&cfg); err == nil {
		t.Fatal("refuseUnpersistedSigningKey = nil with no resolvable home, want an error")
	}
}
