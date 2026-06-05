package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in             string
		wantKey, wantV string
		wantOK         bool
	}{
		{`PODIUM_S3_BUCKET=podium`, "PODIUM_S3_BUCKET", "podium", true},
		{`export OPENAI_API_KEY=sk-abc`, "OPENAI_API_KEY", "sk-abc", true},
		{`  PODIUM_S3_USE_SSL = false `, "PODIUM_S3_USE_SSL", "false", true},
		{`PODIUM_POSTGRES_DSN="postgres://u:p@h:5432/db?sslmode=disable"`, "PODIUM_POSTGRES_DSN", "postgres://u:p@h:5432/db?sslmode=disable", true},
		{`PODIUM_X='quoted value'`, "PODIUM_X", "quoted value", true},
		{`# a comment`, "", "", false},
		{``, "", "", false},
		{`   `, "", "", false},
		{`NOEQUALS`, "", "", false},
		{`=novalue`, "", "", false},
		{`KEY=`, "KEY", "", true},
	}
	for _, c := range cases {
		k, v, ok := parseLine(c.in)
		if k != c.wantKey || v != c.wantV || ok != c.wantOK {
			t.Errorf("parseLine(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, k, v, ok, c.wantKey, c.wantV, c.wantOK)
		}
	}
}

func TestLoadFile_SetsUnsetAndKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	body := "" +
		"# credentials\n" +
		"PODIUM_TESTENV_NEW=fromfile\n" +
		"export PODIUM_TESTENV_EXPORTED=alsofromfile\n" +
		"PODIUM_TESTENV_EXISTING=fromfile\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// An already-set variable must win over the file.
	t.Setenv("PODIUM_TESTENV_EXISTING", "fromenv")
	// Ensure the new vars are absent, and clean them up afterward.
	for _, k := range []string{"PODIUM_TESTENV_NEW", "PODIUM_TESTENV_EXPORTED"} {
		os.Unsetenv(k)
		defer os.Unsetenv(k)
	}

	loadFile(path)

	if got := os.Getenv("PODIUM_TESTENV_NEW"); got != "fromfile" {
		t.Errorf("PODIUM_TESTENV_NEW = %q, want fromfile", got)
	}
	if got := os.Getenv("PODIUM_TESTENV_EXPORTED"); got != "alsofromfile" {
		t.Errorf("PODIUM_TESTENV_EXPORTED = %q, want alsofromfile", got)
	}
	if got := os.Getenv("PODIUM_TESTENV_EXISTING"); got != "fromenv" {
		t.Errorf("PODIUM_TESTENV_EXISTING = %q, want fromenv (existing env must win)", got)
	}
}

func TestLoadFile_MissingIsNoOp(t *testing.T) {
	loadFile(filepath.Join(t.TempDir(), "does-not-exist.env")) // must not panic
}

func TestFindUpward(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "test.env"), []byte("K=v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got := findUpward("test.env"); got != filepath.Join(root, "test.env") {
		t.Errorf("findUpward from %s = %q, want %q", sub, got, filepath.Join(root, "test.env"))
	}
	if got := findUpward("absent.env"); got != "" {
		t.Errorf("findUpward(absent) = %q, want empty (stops at go.mod)", got)
	}
}
