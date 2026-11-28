package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/sync"
)

// syncPolicyText is a policy file with the github instance whose root is
// github.json beside it, and one rule.
func syncPolicyText(rule string) string {
	return `{"version":1,"instances":[{"name":"github","trusted_root_file":"github.json","transparency_log":"none"}],"rules":[` + rule + `]}`
}

// syncRule is a rule of ids under mode with one signer of instance.
func syncRule(ids, mode, identity, instance string) string {
	return `{"ids":["` + ids + `"],"mode":"` + mode + `","requirements":[{"signers":[{"issuer":"` + sh.DefaultIssuer +
		`","identity":"` + identity + `","instance":"` + instance + `"}]}]}`
}

// writePolicyDir writes a policy file and the harness's GitHub-style root
// into dir and returns the policy file's path.
func writePolicyDir(t *testing.T, h *sh.Harness, dir, name, text string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "github.json"), h.TrustedRootJSON(sh.GitHubStyleRoot()...), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// syncInputs returns loader inputs over env, home, and a workspace.
func syncInputs(env map[string]string, home, ws string, stderr *bytes.Buffer) syncAttestationInputs {
	return syncAttestationInputs{ctx: context.Background(), getenv: func(k string) string { return env[k] },
		cwd: ws, startDir: ws, home: home, stderr: stderr, now: time.Now()}
}

// Spec: §7.5 (Author attestation), §4.7.11 (Policy sources) — podium sync
// combines the environment and the user's sync.yaml (user sources) with the
// workspace files (repository sources): a workspace rule takes the user
// default as a floor, the user-only keys of a workspace file are named on
// standard error and ignored, layer_roots merge by layer ID with the
// workspace winning, pin_signers is set by any file, and the overlay comes
// from the user's file.
func TestLoadSyncAttestation_Sources(t *testing.T) {
	h := sh.New(t)
	home, ws := t.TempDir(), t.TempDir()
	writePolicyDir(t, h, filepath.Join(home, ".podium"), "user.json", syncPolicyText(syncRule("finance/**", "require", sh.DefaultSAN, "github")))
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(
		"defaults:\n  attestation:\n    policy: warn\n    policy_file: user.json\n    overlay: allow\n    layer_roots:\n      team: a\n      other: o\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePolicyDir(t, h, filepath.Join(ws, ".podium"), "repo.json", syncPolicyText(syncRule("**", "none", sh.DefaultSAN, "github")))
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.yaml"), []byte(
		"defaults:\n  attestation:\n    policy_file: repo.json\n    overlay: allow\n    root_max_age: 1h\n    trusted_root_file: x.json\n    layer_roots:\n      team: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.local.yaml"), []byte("defaults:\n  attestation:\n    pin_signers: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := writePolicyDir(t, h, filepath.Join(ws, "env"), "env.json", syncPolicyText(syncRule("ops/**", "require", sh.DefaultSAN, "github")))
	var stderr bytes.Buffer
	att, err := loadSyncAttestation(syncInputs(map[string]string{attestPolicyFileEnv: "env/env.json", attestPolicyEnv: " none "}, home, ws, &stderr))
	if err != nil {
		t.Fatalf("load: %v (env file %s)", err, envFile)
	}
	if att.Overlay != sync.OverlayAllow || !att.PinSigners || att.LayerRoots["team"] != "b" || att.LayerRoots["other"] != "o" {
		t.Fatalf("attestation = %+v", att)
	}
	for _, key := range []string{"trusted_root_file", "root_max_age", "overlay"} {
		if !strings.Contains(stderr.String(), "defaults.attestation."+key+" takes effect from ~/.podium/sync.yaml alone") {
			t.Errorf("stderr = %q, want %s named", stderr.String(), key)
		}
	}
	// The repository's none rule takes the user default of warn as a floor;
	// the user's require rule decides finance.
	if r := att.Policy.Resolve("team/x"); r.Mode != attest.ModeWarn {
		t.Errorf("team/x = %s", r)
	}
	if r := att.Policy.Resolve("finance/x"); r.Mode != attest.ModeRequire || r.UserMode != attest.ModeRequire {
		t.Errorf("finance/x = %s", r)
	}
	if r := att.Policy.Resolve("ops/x"); r.Mode != attest.ModeRequire {
		t.Errorf("ops/x = %s", r)
	}
}

// Spec: §7.5 (Author attestation), §13.12 — every setting that does not load
// refuses the invocation with config.attestation_policy_unavailable.
func TestLoadSyncAttestation_Refusals(t *testing.T) {
	h := sh.New(t)
	ws := t.TempDir()
	badHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badHome, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badHome, ".podium", "sync.yaml"), []byte("defaults:\n  attestation:\n    policy: strict\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pg := writePolicyDir(t, h, t.TempDir(), "pg.json", syncPolicyText(syncRule("**", "require", sh.DefaultSAN, "public-good")))
	badWS := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badWS, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badWS, ".podium", "sync.yaml"), []byte("defaults:\n  attestation:\n    policy_file: missing.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		env      map[string]string
		home, ws string
	}{
		"mode":             {env: map[string]string{attestPolicyEnv: "strict"}},
		"limits":           {env: map[string]string{attest.MaxBundlesSetting: "0"}},
		"root timeout":     {env: map[string]string{attestRootTimeoutEnv: "soon"}},
		"env file":         {env: map[string]string{attestPolicyFileEnv: filepath.Join(ws, "missing.json")}},
		"env file parse":   {env: map[string]string{attestPolicyFileEnv: writePolicyDir(t, h, t.TempDir(), "p.json", "{}")}},
		"user block":       {home: badHome},
		"workspace file":   {ws: badWS},
		"root file":        {env: map[string]string{attestPolicyEnv: "require", attestRootFileEnv: filepath.Join(ws, "none.json")}},
		"public-good file": {env: map[string]string{attestPolicyFileEnv: pg, attestRootFileEnv: writePolicyDir(t, h, t.TempDir(), "noct.json", string(h.TrustedRootJSON()))}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadSyncAttestation(syncInputs(tc.env, tc.home, tc.ws, &bytes.Buffer{}))
			if err == nil || !strings.Contains(err.Error(), attest.CodePolicyUnavailable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Spec: §7.5 (Author attestation), §4.7.11 (Trusted roots of a consumer) —
// a rule that names public-good verifies under PODIUM_ATTESTATION_TRUSTED_ROOT_FILE
// for every rule, and a rule of the user's policy file under the
// trusted_root_file of the user's sync.yaml when the environment names none.
func TestLoadSyncAttestation_PublicGoodRoots(t *testing.T) {
	h := sh.New(t)
	pgRoot := writePolicyDir(t, h, t.TempDir(), "pg-root.json", string(h.TrustedRootJSON(sh.WithCTLogKey())))
	pg := writePolicyDir(t, h, t.TempDir(), "pg.json", syncPolicyText(syncRule("**", "require", sh.DefaultSAN, "public-good")))
	att, err := loadSyncAttestation(syncInputs(map[string]string{attestPolicyFileEnv: pg, attestRootFileEnv: pgRoot}, "", "", &bytes.Buffer{}))
	if err != nil || !att.Policy.AboveNone() {
		t.Fatalf("env root: %+v, %v", att, err)
	}

	home := t.TempDir()
	writePolicyDir(t, h, filepath.Join(home, ".podium"), "user.json", syncPolicyText(syncRule("**", "require", sh.DefaultSAN, "public-good")))
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(
		"defaults:\n  attestation:\n    policy_file: user.json\n    trusted_root_file: "+pgRoot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSyncAttestation(syncInputs(nil, home, "", &bytes.Buffer{})); err != nil {
		t.Fatalf("user root file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(
		"defaults:\n  attestation:\n    policy_file: user.json\n    trusted_root_file: missing.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSyncAttestation(syncInputs(nil, home, "", &bytes.Buffer{})); err == nil {
		t.Fatal("a user root file that does not load must refuse")
	}
}

// Spec: §7.5 (Author attestation), §4.7.11 (Maximum age) — with no root
// file a rule naming public-good verifies under the root that the invocation
// refreshes through TUF, falling back to the embedded copy, and the
// root_max_age of the user's sync.yaml bounds its rules: the embedded copy
// of a source build has no root time and is older than every limit.
func TestLoadSyncAttestation_RefreshedRoot(t *testing.T) {
	h := sh.New(t)
	prev := attestTUFMirror
	attestTUFMirror = "http://127.0.0.1:1"
	t.Cleanup(func() { attestTUFMirror = prev })
	home := t.TempDir()
	writePolicyDir(t, h, filepath.Join(home, ".podium"), "user.json", syncPolicyText(syncRule("**", "require", sh.DefaultSAN, "public-good")))
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(
		"defaults:\n  attestation:\n    policy_file: user.json\n    root_max_age: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	att, err := loadSyncAttestation(syncInputs(map[string]string{attestRootTimeoutEnv: "2s"}, home, "", &stderr))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "refresh the public-good root") {
		t.Errorf("stderr = %q, want the failed refresh named", stderr.String())
	}
	m := att.Policy.Resolve("x").Matches[0]
	inst, ok := m.File.Instance(sign.PublicGoodInstance)
	if !ok {
		t.Fatal("no public-good instance")
	}
	if _, err := inst.Root.AttestationRoot(context.Background()); !errors.Is(err, sign.ErrAttestationRootStale) {
		t.Fatalf("root under a 1h limit = %v, want stale", err)
	}
	if st, ok := inst.Root.(sign.AttestationRootStatusSource); !ok || st.Status().Digest == "" {
		t.Fatal("the root reports no status")
	}
	if _, err := (&syncPolicyBuilder{in: syncAttestationInputs{}}).refreshedRoot(0); err == nil {
		t.Fatal("no home directory must refuse a refreshed root")
	}
}

// Spec: §7.5 (Author attestation) — maxAgeRoot applies its own limit to a
// shared root and removes it at zero.
func TestMaxAgeRoot(t *testing.T) {
	prev := attestTUFMirror
	attestTUFMirror = "http://127.0.0.1:1"
	t.Cleanup(func() { attestTUFMirror = prev })
	ci := &checkInstances{ctx: context.Background(), settings: rootSettings{timeout: time.Second}, stderr: &bytes.Buffer{}}
	inst, err := ci.refreshedPublicGoodIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := inst.Root.(*sign.TUFAttestationRoot)
	if _, err := (maxAgeRoot{src: src, now: time.Now}).AttestationRoot(context.Background()); err != nil {
		t.Fatalf("no limit = %v", err)
	}
	if _, err := (maxAgeRoot{src: src, maxAge: time.Hour, now: time.Now}).AttestationRoot(context.Background()); !errors.Is(err, sign.ErrAttestationRootStale) {
		t.Fatalf("1h = %v", err)
	}
}

// Spec: §7.5 (Author attestation) — the loader of one invocation loads once,
// and its configuration error is reported apart from other errors.
func TestNewSyncAttestation_Memoized(t *testing.T) {
	t.Setenv(attestPolicyEnv, "strict")
	t.Setenv("HOME", t.TempDir())
	f := newSyncAttestation(t.TempDir())
	_, err1 := f()
	_, err2 := f()
	if err1 == nil || err1 != err2 {
		t.Fatalf("errors = %v, %v", err1, err2)
	}
	cfg := &sync.AttestationConfigError{Err: err1}
	if !reportAttestationConfigErrorTo(t, cfg) || reportAttestationConfigErrorTo(t, errors.New("other")) {
		t.Fatal("reportAttestationConfigError classifies wrongly")
	}
	var buf bytes.Buffer
	reportAttestationNotes(&buf, []string{"w"}, []sync.SignerChange{{ID: "a"}})
	if buf.String() != "warning: w\nsigner change: a: signers added [], removed []\n" {
		t.Fatalf("notes = %q", buf.String())
	}
}

// reportAttestationConfigErrorTo calls reportAttestationConfigError with the
// standard error discarded.
func reportAttestationConfigErrorTo(t *testing.T, err error) bool {
	t.Helper()
	var ok bool
	withStderr(t, func() { ok = reportAttestationConfigError(err, "") })
	return ok
}

// Spec: §4.7.9 — the attestation report of podium verify: an undecodable
// record under each mode, and a result with its digest and signers.
func TestVerifyAttestationReports(t *testing.T) {
	for mode, want := range map[attest.Mode]int{attest.ModeRequire: 1, attest.ModeWarn: 0, attest.ModeNone: 0} {
		var buf bytes.Buffer
		if got := reportUndecodable(&buf, mode, errors.New("bad")); got != want || !strings.Contains(buf.String(), "mode "+string(mode)) {
			t.Errorf("%s = %d, %q", mode, got, buf.String())
		}
	}
	var buf bytes.Buffer
	reportAttestationResult(&buf, attest.ModeRequire, attest.Result{Checked: true, PackageDigest: "sha256:1",
		Accepted: []attest.AcceptedBundle{{Digest: "sha256:2", Instance: "github", Issuer: "i", Identity: "x", AttestedTime: time.Unix(0, 0)}, {Digest: "sha256:3"}}})
	out := buf.String()
	if !strings.Contains(out, "attestation: ok") || !strings.Contains(out, "package_digest: sha256:1") ||
		!strings.Contains(out, `requirement 1: bundle sha256:2 signer instance="github"`) || !strings.Contains(out, "attested_time=none") {
		t.Fatalf("report = %q", out)
	}
	buf.Reset()
	reportAttestationResult(&buf, attest.ModeWarn, attest.Result{Checked: true, Resolution: attest.Resolution{ID: "a"},
		Warnings: []*attest.Failure{attest.NewFailure(attest.CodeAttestationMissing, "", "")}})
	if !strings.Contains(buf.String(), "attestation: warning: a: materialize.attestation_missing") {
		t.Fatalf("warn report = %q", buf.String())
	}
	buf.Reset()
	reportAttestationResult(&buf, attest.ModeNone, attest.Result{})
	if !strings.Contains(buf.String(), "not checked") {
		t.Fatalf("none report = %q", buf.String())
	}
}
