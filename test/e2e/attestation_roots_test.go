package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// readSignTestdata reads a trusted root committed under pkg/sign.
func readSignTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "sign", "testdata", "attestation", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// writeE2EFile writes raw under dir and returns the path.
func writeE2EFile(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// rootDigest is the §4.7.11 digest of a trusted root document.
func rootDigest(t *testing.T, raw []byte) string {
	t.Helper()
	r, err := sign.ParseAttestationRoot(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r.Digest()
}

// Spec: §4.7.11, §13.12 — through the binary, each attestation start check
// refuses the start with config.invalid and a message that names the
// setting or the instance: an unreadable PODIUM_ATTESTATION_TRUSTED_ROOT_FILE,
// one that holds two roots, one with a CA-first timestamp authority entry,
// one with no certificate transparency log, a refresh interval, refresh
// deadline, or maximum age that §13.12 does not admit, an unreadable root
// file of a sigstore.instances entry, and an entry named public-good.
// Matrix: §6.10 (config.invalid)
func TestAttestationRoots_StartupRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := sh.New(t)
	var line bytes.Buffer
	if err := json.Compact(&line, readSignTestdata(t, "public-good.trusted_root.json")); err != nil {
		t.Fatal(err)
	}
	twoRoots := writeE2EFile(t, dir, "two.json", append(append(line.Bytes(), '\n'), line.Bytes()...))
	caFirst := writeE2EFile(t, dir, "cafirst.json", h.TrustedRootJSON(sh.WithCTLogKey(), sh.WithCAFirstTSA()))
	noCT := writeE2EFile(t, dir, "noct.json", h.TrustedRootJSON())
	missingInstanceRoot := writeE2EFile(t, dir, "missing-root.yaml",
		[]byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: absent-github.json\n"))
	publicGoodEntry := writeE2EFile(t, dir, "public-good.yaml",
		[]byte("registry:\n  sigstore:\n    instances:\n      - name: public-good\n        trusted_root: "+noCT+"\n"))

	for _, tc := range []struct {
		name string
		env  []string
		want []string
	}{
		{"unreadable root file", []string{"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + filepath.Join(dir, "absent.json")},
			[]string{"config.invalid", "PODIUM_ATTESTATION_TRUSTED_ROOT_FILE", "absent.json"}},
		{"two roots", []string{"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + twoRoots},
			[]string{"config.invalid", "two.json", "holds 2 trusted roots"}},
		{"CA-first timestamp authority", []string{"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + caFirst},
			[]string{"config.invalid", "cafirst.json", "CA certificate"}},
		{"no certificate transparency log", []string{"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + noCT},
			[]string{"config.invalid", "noct.json", "no certificate transparency log"}},
		{"refresh interval", []string{"PODIUM_SIGSTORE_ROOT_REFRESH=0s"},
			[]string{"config.invalid: PODIUM_SIGSTORE_ROOT_REFRESH must be a duration above zero"}},
		{"refresh interval without a unit", []string{"PODIUM_SIGSTORE_ROOT_REFRESH=24"},
			[]string{"config.invalid: PODIUM_SIGSTORE_ROOT_REFRESH must be a duration above zero"}},
		{"refresh deadline", []string{"PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT=-1s"},
			[]string{"config.invalid: PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT must be a duration above zero"}},
		{"maximum age", []string{"PODIUM_SIGSTORE_ROOT_MAX_AGE=90d"},
			[]string{"config.invalid: PODIUM_SIGSTORE_ROOT_MAX_AGE must be a duration of zero or more"}},
		{"unreadable instance root", []string{"PODIUM_CONFIG_FILE=" + missingInstanceRoot},
			[]string{"config.invalid", `Sigstore instance "github"`, "absent-github.json"}},
		{"public-good entry", []string{"PODIUM_CONFIG_FILE=" + publicGoodEntry},
			[]string{"config.invalid", "the public-good instance is built in"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := append([]string{"HOME=" + t.TempDir(), "PODIUM_SIGN=none"}, tc.env...)
			out := runPodium(t, "", env, "serve", "--standalone", "--bind", "127.0.0.1:0")
			combined := out.Stderr + out.Stdout
			if out.Exit == 0 {
				t.Fatalf("serve started, want a refusal:\n%s", combined)
			}
			for _, want := range tc.want {
				if !strings.Contains(combined, want) {
					t.Errorf("output lacks %q:\n%s", want, combined)
				}
			}
		})
	}
}

// Spec: §4.7.11, §6.2 — through the binary, the attestation path does not
// read PODIUM_SIGSTORE_TRUSTED_ROOT_FILE: a start with it naming an
// unreadable file starts, and a start with it naming the root of another
// instance reports the embedded public-good root and never that file's.
func TestAttestationRoots_IgnoresKeylessTrustedRootFile(t *testing.T) {
	t.Parallel()
	ghRoot := readSignTestdata(t, "github.trusted_root.json")
	ghPath := writeE2EFile(t, t.TempDir(), "github.json", ghRoot)
	embedded := rootDigest(t, sign.EmbeddedPublicGoodRoot())
	for name, value := range map[string]string{
		"unreadable":     filepath.Join(t.TempDir(), "absent.json"),
		"other instance": ghPath,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := startServerArgs(t, []string{
				"HOME=" + t.TempDir(), "PODIUM_SIGN=none",
				"PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=" + value,
			}, "serve", "--standalone")
			log := srv.log()
			want := "attestation: instance public-good: trusted root " + embedded + " from embedded"
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %q:\n%s", want, log)
			}
			if strings.Contains(log, rootDigest(t, ghRoot)) {
				t.Errorf("log reports the digest of the PODIUM_SIGSTORE_TRUSTED_ROOT_FILE root:\n%s", log)
			}
		})
	}
}

// Spec: §4.7.11, §13.12 — through the binary, a start with a static
// public-good root file and a github instance reports the digest and root
// time of each root, from the file, warns for a timestamp authority whose
// chain holds a SHA-1 signature, and `podium config show --server` lists
// the instances and the settings.
func TestAttestationRoots_StartReportsInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := sh.New(t)
	pgRoot := readSignTestdata(t, "public-good.trusted_root.json")
	pgPath := writeE2EFile(t, dir, "public-good.json", pgRoot)
	ghRoot := h.TrustedRootJSON(append(sh.GitHubStyleRoot(), sh.WithTimestampAuthorities(sh.TSASHA1))...)
	writeE2EFile(t, dir, "github.json", ghRoot)
	cfg := writeE2EFile(t, dir, "registry.yaml",
		[]byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: github.json\n"))
	env := []string{
		"HOME=" + t.TempDir(), "PODIUM_SIGN=none", "PODIUM_CONFIG_FILE=" + cfg,
		"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + pgPath,
	}
	srv := startServerArgs(t, env, "serve", "--standalone")
	log := srv.log()
	for _, want := range []string{
		"attestation: instance public-good: trusted root " + rootDigest(t, pgRoot) + " from file (" + pgPath + "), root time ",
		"attestation: instance github: trusted root " + rootDigest(t, ghRoot) + " from file (" + filepath.Join(dir, "github.json") + ")",
		"warning: attestation: instance github: the chain of timestamp authority",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "root time unknown") {
		t.Errorf("a file root reported an unknown root time:\n%s", log)
	}

	out := runPodium(t, "", env, "config", "show", "--server")
	cliWantExit(t, out, 0, "config show --server")
	for _, want := range []string{
		"sigstore.instances", "public-good,github",
		"sigstore.instances.github", "transparency_log=false",
		"attestation.trusted_root_file", pgPath,
		"sigstore.root_refresh", "24h0m0s",
		"sigstore.root_max_age", "2160h0m0s",
	} {
		cliContains(t, out.Stdout, want, "config show --server")
	}
}

// Spec: §13.12 — through the binary, `podium config show --server` marks
// an attestation setting the registry refuses to start with and prints the
// refusal as a warning.
func TestAttestationRoots_ConfigShowMarksInvalid(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT=30"},
		"config", "show", "--server")
	cliWantExit(t, out, 0, "config show --server with an invalid refresh deadline")
	cliContains(t, out.Stderr, "warning: config.invalid: PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT must be a duration above zero", "config show warning")
	cliContains(t, out.Stdout, "30 (invalid)", "config show settings")
}
