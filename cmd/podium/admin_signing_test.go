package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// runKeyTool calls one signing-key subcommand with captured output.
func runKeyTool(fn func([]string, *bytes.Buffer, *bytes.Buffer) int, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := fn(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func generate(args []string, stdout, stderr *bytes.Buffer) int {
	return signingKeyGenerate(args, stdout, stderr)
}

func rotate(args []string, stdout, stderr *bytes.Buffer) int {
	return signingKeyRotate(args, stdout, stderr)
}

func readKeyFileT(t *testing.T, path string) sign.KeyFile {
	t.Helper()
	kf, err := sign.ReadKeyFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	return kf
}

func b64(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }

// wantKeySetOutput is the stdout the key tool prints for a key file.
func wantKeySetOutput(kf sign.KeyFile) string {
	keys := append([]ed25519.PublicKey{kf.Public}, kf.Verify...)
	enc := make([]string, len(keys))
	var lines []string
	for i, k := range keys {
		enc[i] = b64(k)
		role := "verify"
		if i == 0 {
			role = "signing"
		}
		lines = append(lines, "key_id="+sign.KeyIDFor(k)+" role="+role)
	}
	return strings.Join(append([]string{strings.Join(enc, ",")}, lines...), "\n") + "\n"
}

// Spec: §4.7.9 — generate writes a 0600 key file whose private: and public:
// lines match, prints its verification key set and key_id, and refuses to
// replace an existing file, leaving it byte-identical.
func TestSigningKeyGenerate_WritesAndRefusesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "registry-signing.key")
	code, out, errOut := runKeyTool(generate, "--key-file", path)
	if code != 0 {
		t.Fatalf("generate exit = %d, stderr %s", code, errOut)
	}
	kf := readKeyFileT(t, path)
	if kf.Private == nil || len(kf.Verify) != 0 {
		t.Fatalf("generated file: private %v, verify %d lines", kf.Private != nil, len(kf.Verify))
	}
	if out != wantKeySetOutput(kf) {
		t.Errorf("stdout = %q, want %q", out, wantKeySetOutput(kf))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v (err %v), want 0600", info.Mode().Perm(), err)
	}

	before, _ := os.ReadFile(path)
	code, _, errOut = runKeyTool(generate, "--key-file", path)
	after, _ := os.ReadFile(path)
	if code != 1 || !strings.Contains(errOut, "already exists") || !bytes.Equal(before, after) {
		t.Errorf("second generate: exit %d, stderr %q, file changed %v", code, errOut, !bytes.Equal(before, after))
	}
}

// Spec: §4.7.9 — rotate keeps the previous public key and every previous
// verify: key as verify: lines, in that order, under a new signing key.
func TestSigningKeyRotate_CarriesThePreviousKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if code, _, errOut := runKeyTool(generate, "--key-file", path); code != 0 {
		t.Fatalf("generate: %s", errOut)
	}
	original := readKeyFileT(t, path)

	code, out, errOut := runKeyTool(rotate, "--key-file", path)
	if code != 0 {
		t.Fatalf("rotate exit = %d, stderr %s", code, errOut)
	}
	first := readKeyFileT(t, path)
	if first.Public.Equal(original.Public) || len(first.Verify) != 1 || !first.Verify[0].Equal(original.Public) {
		t.Fatalf("after one rotation: verify %d lines, want the original key", len(first.Verify))
	}
	wantFirstLine := b64(first.Public) + "," + b64(original.Public)
	if !strings.HasPrefix(out, wantFirstLine+"\n") || out != wantKeySetOutput(first) {
		t.Errorf("stdout = %q, want %q", out, wantKeySetOutput(first))
	}

	if code, _, errOut := runKeyTool(rotate, "--key-file", path); code != 0 {
		t.Fatalf("second rotate: %s", errOut)
	}
	second := readKeyFileT(t, path)
	if len(second.Verify) != 2 || !second.Verify[0].Equal(first.Public) || !second.Verify[1].Equal(original.Public) {
		t.Errorf("after two rotations: verify lines are not [first, original]")
	}
}

// Spec: §4.7.9 — --staged-out writes the previous file unchanged plus one
// verify: line for the new signing key, with mode 0600.
func TestSigningKeyRotate_StagedOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry-signing.key")
	staged := filepath.Join(dir, "staged.key")
	if code, _, errOut := runKeyTool(generate, "--key-file", path); code != 0 {
		t.Fatalf("generate: %s", errOut)
	}
	if code, _, errOut := runKeyTool(rotate, "--key-file", path); code != 0 {
		t.Fatalf("rotate: %s", errOut)
	}
	prev := readKeyFileT(t, path)

	if code, _, errOut := runKeyTool(rotate, "--key-file", path, "--staged-out", staged); code != 0 {
		t.Fatalf("rotate --staged-out: %s", errOut)
	}
	next := readKeyFileT(t, path)
	s := readKeyFileT(t, staged)
	if !s.Private.Equal(prev.Private) || !s.Public.Equal(prev.Public) {
		t.Error("staged file changed the previous private: or public: line")
	}
	if len(s.Verify) != 2 || !s.Verify[0].Equal(prev.Verify[0]) || !s.Verify[1].Equal(next.Public) {
		t.Error("staged verify: lines are not the previous ones plus the new public key")
	}
	if info, err := os.Stat(staged); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("staged mode = %v (err %v), want 0600", info.Mode().Perm(), err)
	}
}

// Spec: §4.7.9 — both subcommands require --key-file and read no default
// path; rotate refuses an absent file and a staged file that names the key
// file, writing no file.
func TestSigningKey_Refusals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODIUM_SIGN_KEY_PATH", filepath.Join(home, "env.key"))
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.key")
	staged := filepath.Join(dir, "staged.key")

	cases := []struct {
		name string
		fn   func([]string, *bytes.Buffer, *bytes.Buffer) int
		args []string
		code int
		want string
	}{
		{"generate without --key-file", generate, nil, 2, "--key-file is required"},
		{"rotate without --key-file", rotate, nil, 2, "--key-file is required"},
		{"rotate with a positional", rotate, []string{"--key-file", absent, "extra"}, 2, "unexpected argument"},
		{"rotate with an unknown flag", rotate, []string{"--bogus"}, 2, "bogus"},
		{"rotate an absent file", rotate, []string{"--key-file", absent, "--staged-out", staged}, 1, absent},
		{"rotate onto itself", rotate, []string{"--key-file", absent, "--staged-out", absent}, 1, "--staged-out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runKeyTool(tc.fn, tc.args...)
			if code != tc.code || !strings.Contains(errOut, tc.want) || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d and %q", code, out, errOut, tc.code, tc.want)
			}
		})
	}
	for _, p := range []string{absent, staged, filepath.Join(home, "env.key"), filepath.Join(home, ".podium")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was created", p)
		}
	}
}

// The dispatcher prints its help for no subcommand and for --help, and
// refuses an unknown subcommand.
func TestAdminSigningKeyCmd_Dispatch(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2},
		{[]string{"--help"}, 0},
		{[]string{"bogus"}, 2},
		{[]string{"generate"}, 2},
		{[]string{"rotate"}, 2},
	}
	for _, tc := range cases {
		if code := adminSigningKeyCmd(tc.args); code != tc.code {
			t.Errorf("signing-key %v exit = %d, want %d", tc.args, code, tc.code)
		}
	}
	if code := adminCmd([]string{"signing-key", "--help"}); code != 0 {
		t.Errorf("admin signing-key --help exit = %d, want 0", code)
	}
	if code := adminCmd([]string{"sign-stored-rows", "extra-arg"}); code != 2 {
		t.Errorf("admin sign-stored-rows extra-arg exit = %d, want 2", code)
	}
}

// Spec: §4.7.9 — a key file that cannot be written fails with exit status 1:
// generate under a parent that is a regular file, and rotate whose staged
// file cannot be written, which leaves the rotated file unchanged.
func TestSigningKey_WriteFailures(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runKeyTool(generate, "--key-file", filepath.Join(blocker, "k.key")); code != 1 || errOut == "" {
		t.Errorf("generate under a file: exit %d, stderr %q; want 1", code, errOut)
	}

	path := filepath.Join(dir, "registry-signing.key")
	if code, _, errOut := runKeyTool(generate, "--key-file", path); code != 0 {
		t.Fatalf("generate: %s", errOut)
	}
	before, _ := os.ReadFile(path)
	code, out, errOut := runKeyTool(rotate, "--key-file", path, "--staged-out", filepath.Join(blocker, "staged.key"))
	after, _ := os.ReadFile(path)
	if code != 1 || out != "" || errOut == "" || !bytes.Equal(before, after) {
		t.Errorf("rotate with an unwritable staged file: exit %d, stdout %q, key file changed %v", code, out, !bytes.Equal(before, after))
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		return
	}
	if code, _, _ := runKeyTool(rotate, "--key-file", path); code != 1 {
		t.Errorf("rotate in a read-only directory: exit %d, want 1", code)
	}
	if code, _, _ := runKeyTool(generate, "--key-file", filepath.Join(dir, "new.key")); code != 1 {
		t.Errorf("generate in a read-only directory: exit %d, want 1", code)
	}
}
