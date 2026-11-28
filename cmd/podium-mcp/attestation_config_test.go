package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// githubRoot writes a github-style trusted root and returns its path.
func githubRoot(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "github-root.json")
	writeFiles(t, filepath.Dir(p), map[string]string{"github-root.json": string(sh.New(t).TrustedRootJSON(sh.GitHubStyleRoot()...))})
	return p
}

// publicGoodPolicy is a policy file whose require rule names a signer on
// the public-good instance.
const publicGoodPolicy = `{"version":1,"rules":[{"ids":["**"],"mode":"require","requirements":[{"signers":[` +
	`{"issuer":"https://accounts.acme.com","identity":"alice@acme.com","instance":"public-good"}]}]}]}`

// wantPolicyRefusal fails unless err is a config.attestation_policy_unavailable
// refusal that mentions part.
func wantPolicyRefusal(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), attest.CodePolicyUnavailable) || !strings.Contains(err.Error(), part) {
		t.Fatalf("err = %v, want %s naming %q", err, attest.CodePolicyUnavailable, part)
	}
}

// Spec: §4.7.11 (Policy sources, User sources of the MCP server) — every
// source contributes: a user default of require in ~/.podium/sync.yaml is a
// floor on the none rule of a file the environment names, and a rule of the
// user's own file keeps its mode.
func TestConsumerAttestation_UserDefaultIsAFloor(t *testing.T) {
	t.Parallel()
	root := githubRoot(t)
	e := newConsumerEnv(t)
	writeFiles(t, e.home, map[string]string{".podium/user.json": policyJSON(root, "require", sh.DefaultSAN)})
	e.writeUserSync(t, "defaults:\n  attestation:\n    policy: require\n    policy_file: user.json\n")
	repo := filepath.Join(t.TempDir(), "repo.json")
	writeFiles(t, filepath.Dir(repo), map[string]string{"repo.json": `{"version":1,"rules":[{"ids":["**"],"mode":"none"}]}`})
	e.env[envAttestationPolicyFile] = repo
	e.flagMode = "none"
	a := e.mustLoad(t)
	for id, want := range map[string]attest.Mode{"finance/pay": attest.ModeRequire, "personal/x": attest.ModeRequire} {
		if got := a.policy.Resolve(id); got.Mode != want || got.UserMode != attest.ModeRequire {
			t.Errorf("%s: %v, want mode %s and user mode require", id, got, want)
		}
	}
	if len(a.refreshers) != 0 {
		t.Errorf("a policy with no public-good signer built %d refreshers", len(a.refreshers))
	}
}

// Spec: §4.7.11 (User sources of the MCP server) — a workspace sync.yaml is a
// repository source: its policy_file contributes, resolved against its own
// directory, and its trusted_root_file, root_max_age, and overlay are
// ignored and named on standard error.
func TestConsumerAttestation_WorkspaceFile(t *testing.T) {
	t.Parallel()
	root := githubRoot(t)
	e := newConsumerEnv(t)
	writeFiles(t, e.cwd, map[string]string{
		".podium/sync.yaml": "defaults:\n  attestation:\n    policy_file: ws.json\n    trusted_root_file: x.json\n    root_max_age: 1h\n    overlay: allow\n",
		".podium/ws.json":   policyJSON(root, "warn", sh.DefaultSAN),
	})
	a := e.mustLoad(t)
	if got := a.policy.Resolve("finance/pay"); got.Mode != attest.ModeWarn || got.UserMode != attest.ModeNone {
		t.Errorf("resolution = %v", got)
	}
	if a.overlay != "" {
		t.Errorf("a workspace file set overlay %q", a.overlay)
	}
	for _, k := range []string{"trusted_root_file", "root_max_age", "overlay"} {
		if !strings.Contains(e.stderr.String(), "defaults.attestation."+k) {
			t.Errorf("stderr does not name %s: %q", k, e.stderr.String())
		}
	}
}

// Spec: §4.7.11 (Policy load, User sources of the MCP server) — the start
// is refused for a source that cannot be loaded.
// Matrix: §6.10 (config.attestation_policy_unavailable)
func TestConsumerAttestation_StartRefusals(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		set  func(t *testing.T, e *consumerEnv)
		part string
	}{
		"missing env policy file":  {func(_ *testing.T, e *consumerEnv) { e.env[envAttestationPolicyFile] = "/nonexistent/p.json" }, "/nonexistent/p.json"},
		"missing flag policy file": {func(_ *testing.T, e *consumerEnv) { e.flagFile = "/nonexistent/f.json" }, "the podium-mcp configuration"},
		"unknown env mode":         {func(_ *testing.T, e *consumerEnv) { e.env[envAttestationPolicy] = "strict" }, "strict"},
		"bad attestation block": {func(t *testing.T, e *consumerEnv) {
			e.writeUserSync(t, "defaults:\n  attestation:\n    polcy: require\n")
		}, "polcy"},
		"user sync.yaml does not parse": {func(t *testing.T, e *consumerEnv) { e.writeUserSync(t, "defaults: [\n") }, "sync.yaml"},
		"bad root_max_age": {func(t *testing.T, e *consumerEnv) {
			e.writeUserSync(t, "defaults:\n  attestation:\n    root_max_age: soon\n")
		}, "root_max_age"},
		"bad refresh": {func(_ *testing.T, e *consumerEnv) { e.env[envRootRefresh] = "0s" }, envRootRefresh},
		"bad max bytes": {func(_ *testing.T, e *consumerEnv) {
			e.env[attest.MaxBytesSetting] = "0"
		}, attest.MaxBytesSetting},
		"unreadable env root under require": {func(_ *testing.T, e *consumerEnv) {
			e.env[envAttestationPolicy] = "require"
			e.env[envAttestationRootFile] = "/nonexistent/root.json"
		}, envAttestationRootFile},
		"machine file that does not parse": {func(t *testing.T, e *consumerEnv) {
			writeFiles(t, e.machineDir, map[string]string{"sync.yaml": "defaults: [\n"})
		}, "machine sync.yaml"},
		"workspace attestation block of another type": {func(t *testing.T, e *consumerEnv) {
			writeFiles(t, e.cwd, map[string]string{".podium/sync.local.yaml": "defaults:\n  attestation: require\n"})
		}, "sync.local.yaml"},
		"public-good root file that is no root": {func(t *testing.T, e *consumerEnv) {
			writeFiles(t, e.home, map[string]string{".podium/p.json": publicGoodPolicy, ".podium/root.json": "{}"})
			e.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: p.json\n    trusted_root_file: root.json\n")
		}, "public-good root"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newConsumerEnv(t)
			tc.set(t, e)
			_, err := e.load()
			wantPolicyRefusal(t, err, tc.part)
		})
	}
	// Without a policy above none, an unreadable root file is not read.
	e := newConsumerEnv(t)
	e.env[envAttestationRootFile] = "/nonexistent/root.json"
	e.mustLoad(t)
}

// Spec: §4.7.11 (User sources of the MCP server, Machine file) — the
// machine file applies as a user source, and the overlay values of the
// user's file and the machine file combine to the strictest.
func TestConsumerAttestation_MachineFileAndOverlay(t *testing.T) {
	t.Parallel()
	root := githubRoot(t)
	for _, tc := range []struct{ machine, user, want string }{
		{"allow", "", "allow"},
		{"allow", "deny", "deny"},
		{"deny", "allow", "deny"},
		{"", "", ""},
	} {
		e := newConsumerEnv(t)
		m := "defaults:\n  attestation:\n    policy: require\n    policy_file: m.json\n"
		if tc.machine != "" {
			m += "    overlay: " + tc.machine + "\n"
		}
		writeFiles(t, e.machineDir, map[string]string{"sync.yaml": m, "m.json": policyJSON(root, "require", sh.DefaultSAN)})
		if tc.user != "" {
			e.writeUserSync(t, "defaults:\n  attestation:\n    overlay: "+tc.user+"\n")
		}
		a := e.mustLoad(t)
		if a.overlay != tc.want {
			t.Errorf("machine %q, user %q: overlay %q, want %q", tc.machine, tc.user, a.overlay, tc.want)
		}
		if got := a.policy.Resolve("finance/pay"); got.UserMode != attest.ModeRequire {
			t.Errorf("the machine file's rule does not apply: %v", got)
		}
		if len(a.notices) != 0 {
			t.Errorf("notices = %v", a.notices)
		}
	}
}

// Spec: §4.7.11 (Root copy of user-source rules, User sources of the MCP
// server) — a user-source rule on public-good verifies against the root
// copy under the account home, a repository-source rule shares it with no
// root setting, gets a read-only refresh of its own under a refresh
// setting, and a root file of its own under PODIUM_ATTESTATION_TRUSTED_ROOT_FILE;
// with no account home the refreshed root stays in memory.
func TestConsumerAttestation_PublicGoodRoots(t *testing.T) {
	t.Parallel()
	pgRoot := filepath.Join(t.TempDir(), "pg.json")
	writeFiles(t, filepath.Dir(pgRoot), map[string]string{"pg.json": string(sh.New(t).TrustedRootJSON(sh.WithCTLogKey()))})

	e := newConsumerEnv(t)
	writeFiles(t, e.home, map[string]string{".podium/p.json": publicGoodPolicy})
	e.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: p.json\n    root_max_age: 0s\n")
	repo := filepath.Join(t.TempDir(), "repo.json")
	writeFiles(t, filepath.Dir(repo), map[string]string{"repo.json": publicGoodPolicy})
	e.env[envAttestationPolicyFile] = repo
	a := e.mustLoad(t)
	if len(a.refreshers) != 1 {
		t.Fatalf("refreshers = %d, want the shared user copy", len(a.refreshers))
	}
	if _, err := os.Stat(filepath.Join(e.home, rootCopyDir)); !os.IsNotExist(err) {
		t.Errorf("building the root wrote the copy directory: %v", err)
	}

	e.env[envRootRefresh] = "48h"
	if a = e.mustLoad(t); len(a.refreshers) != 2 {
		t.Errorf("refreshers = %d, want a read-only refresh for repository-source rules", len(a.refreshers))
	}

	delete(e.env, envRootRefresh)
	e.env[envAttestationRootFile] = pgRoot
	if a = e.mustLoad(t); len(a.refreshers) != 1 {
		t.Errorf("refreshers = %d, want the user copy alone beside a root file", len(a.refreshers))
	}

	n := newConsumerEnv(t)
	n.home, n.homeErr = "", os.ErrNotExist
	n.env[envAttestationPolicyFile] = repo
	n.env[envRootMaxAge] = "1h"
	if a = n.mustLoad(t); len(a.refreshers) != 1 || len(a.notices) != 1 {
		t.Errorf("no home: refreshers %d, notices %v", len(a.refreshers), a.notices)
	}

	u := newConsumerEnv(t)
	writeFiles(t, u.home, map[string]string{".podium/p.json": publicGoodPolicy})
	u.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: p.json\n    trusted_root_file: "+pgRoot+"\n")
	if a = u.mustLoad(t); len(a.refreshers) != 0 {
		t.Errorf("a user root file built %d refreshers", len(a.refreshers))
	}
}

// Spec: §4.7.11 (Test seam) — a release build refuses each hidden test flag
// as undefined, in every spelling, and reads no other flag as one.
func TestTestSeam_ReleaseBuildRefusesTheFlags(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--test-account-home", "/tmp"}, {"-test-account-home=/tmp"}, {"--test-machine-config-dir"},
		{"--registry", "http://x", "--test-machine-config-dir=/x"},
	} {
		if err := applyTestSeam(args, &systemPaths{}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if err := applyTestSeam([]string{"--registry", "http://x", "-test.v", "--test-account-homes", "x"}, &systemPaths{}); err != nil {
		t.Errorf("an unrelated flag was refused: %v", err)
	}
}

// Spec: §4.7.11 (User home) — the account home comes from the account
// database, whatever HOME holds.
func TestAccountHome_IgnoresHOME(t *testing.T) {
	want, err := accountHome()
	if err != nil {
		t.Skipf("the account database has no entry for this user: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	got, err := accountHome()
	if err != nil || got != want {
		t.Fatalf("accountHome = %q, %v; want %q", got, err, want)
	}
	if p := productionPaths(); p.accountHome == nil || p.machine.Dir != "" {
		t.Errorf("productionPaths = %+v", p)
	}
}

// Spec: §4.7.11 — the failure envelope of a check carries the code, the
// constraint, and the requested ID, and a non-attestation error keeps the
// code its message leads with.
func TestVerifyFailureResult(t *testing.T) {
	t.Parallel()
	err := attest.CheckRequestedID("GIT~1")
	m := verifyFailureResult(err, "GIT~1")
	if m["code"] != attest.CodeRequestMismatch || strings.HasPrefix(m["message"].(string), "(") {
		t.Errorf("envelope = %v", m)
	}
	if m := verifyFailureResult(sign.ErrSignatureInvalid, "x"); m["error"] == nil {
		t.Errorf("plain error = %v", m)
	}
	if !cacheMiss(err) || cacheMiss(os.ErrNotExist) {
		t.Error("cacheMiss misreads a failure")
	}
}

// Spec: §6.10 — the consumer verification codes carry their retryable flag
// and a remediation hint; only a stale root is retryable.
func TestBridgeCodeMeta_AttestationCodes(t *testing.T) {
	t.Parallel()
	for _, code := range []string{attest.CodeRequestMismatch, attest.CodeAttestationMissing, attest.CodeAttestationInvalid,
		attest.CodeAttestationUntrusted, attest.CodeAttestationMismatch, attest.CodeAttestationRootStale, attest.CodePolicyUnavailable} {
		retryable, hint := bridgeCodeMeta(code)
		if hint == "" || retryable != (code == attest.CodeAttestationRootStale) {
			t.Errorf("%s: retryable %v, hint %q", code, retryable, hint)
		}
	}
}

// Spec: §4.7.11 (Known keys) — in the user's own sync.yaml and the machine
// file, a protective block written at the top level, as
// defaults.attestations, or under default refuses the start and names the
// key and the file, rather than being dropped.
func TestConsumerAttestation_MisplacedBlockRefusesStart(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"top level": "attestation:\n  policy: require\n",
		"plural":    "defaults:\n  attestations:\n    policy: require\n",
		"default":   "default:\n  attestation:\n    policy: require\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newConsumerEnv(t)
			e.writeUserSync(t, body)
			_, err := e.load()
			wantPolicyRefusal(t, err, filepath.Join(e.home, ".podium", "sync.yaml"))
			m := newConsumerEnv(t)
			writeFiles(t, m.machineDir, map[string]string{"sync.yaml": body})
			_, err = m.load()
			wantPolicyRefusal(t, err, "unknown key")
		})
	}
}

// Spec: §4.7.11 (Keys) — a workspace sync.yaml that does not parse refuses
// the start only in the working directory and when the user owns it; an
// ancestor's file, such as /tmp/.podium/sync.yaml above a directory under
// /tmp, is ignored and reported as workspace_file in every load result. The
// refusal names the file once.
func TestConsumerAttestation_UnparseableWorkspaceFile(t *testing.T) {
	t.Parallel()
	e := newConsumerEnv(t)
	shared := t.TempDir()
	writeFiles(t, shared, map[string]string{".podium/sync.yaml": "defaults: [\n"})
	e.cwd = filepath.Join(shared, "alice-scratch", "sub")
	writeFiles(t, e.cwd, map[string]string{"x": ""})
	a := e.mustLoad(t)
	if len(a.notices) != 1 || !strings.Contains(a.notices[0], "constraint workspace_file") {
		t.Fatalf("notices = %v", a.notices)
	}
	if !strings.Contains(e.stderr.String(), "ignoring the workspace file") {
		t.Errorf("stderr = %q", e.stderr.String())
	}

	w := newConsumerEnv(t)
	writeFiles(t, w.cwd, map[string]string{".podium/sync.local.yaml": "defaults: [\n"})
	_, err := w.load()
	path := filepath.Join(w.cwd, ".podium", "sync.local.yaml")
	wantPolicyRefusal(t, err, path)
	if n := strings.Count(err.Error(), path); n != 1 {
		t.Errorf("the refusal names the file %d times: %v", n, err)
	}
}

// Spec: §4.7.11 (Keys) — every refusal of a workspace file, an attestation
// block that does not decode, a bad mode, and a policy file that does not
// load as well as a file that does not parse, applies only to the user's own
// file in the working directory: the same file in an ancestor, a linked
// file, and a file under a linked .podium are ignored with workspace_file.
func TestConsumerAttestation_EveryWorkspaceRefusalIsLocal(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unparseable":   "defaults: [\n",
		"bad block key": "defaults:\n  attestation:\n    bogus: 1\n",
		"bad mode":      "defaults:\n  attestation:\n    policy: bogus\n",
		"missing file":  "defaults:\n  attestation:\n    policy_file: /nonexistent/p.json\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ancestor := newConsumerEnv(t)
			shared := t.TempDir()
			writeFiles(t, shared, map[string]string{".podium/sync.yaml": body})
			ancestor.cwd = filepath.Join(shared, "alice", "sub")
			writeFiles(t, ancestor.cwd, map[string]string{"x": ""})
			if a := ancestor.mustLoad(t); len(a.notices) != 1 || !strings.Contains(a.notices[0], "workspace_file") {
				t.Errorf("ancestor: notices = %v", a.notices)
			}

			own := newConsumerEnv(t)
			writeFiles(t, own.cwd, map[string]string{".podium/sync.yaml": body})
			_, err := own.load()
			wantPolicyRefusal(t, err, "")

			if runtime.GOOS == "windows" {
				return
			}
			linkedFile := newConsumerEnv(t)
			target := filepath.Join(t.TempDir(), "sync.yaml")
			writeFiles(t, filepath.Dir(target), map[string]string{"sync.yaml": body})
			writeFiles(t, linkedFile.cwd, map[string]string{".podium/keep": ""})
			if err := os.Symlink(target, filepath.Join(linkedFile.cwd, ".podium", "sync.yaml")); err != nil {
				t.Fatal(err)
			}
			if a := linkedFile.mustLoad(t); len(a.notices) != 1 {
				t.Errorf("linked file: notices = %v", a.notices)
			}
			linkedDir := newConsumerEnv(t)
			if err := os.Symlink(filepath.Dir(filepath.Join(shared, ".podium", "sync.yaml")), filepath.Join(linkedDir.cwd, ".podium")); err != nil {
				t.Fatal(err)
			}
			if a := linkedDir.mustLoad(t); len(a.notices) != 1 {
				t.Errorf("linked .podium: notices = %v", a.notices)
			}
		})
	}
}

// Spec: §6.2, §4.7.11 (Repository-source roots) — the bundle deadline is
// operator-tunable through PODIUM_ATTESTATION_FETCH_TIMEOUT, and the root
// refresh interval keeps the registry's floor.
func TestConsumerAttestation_FetchTimeoutAndRefreshFloor(t *testing.T) {
	t.Parallel()
	e := newConsumerEnv(t)
	e.env[envFetchTimeout] = " 5s "
	a := e.mustLoad(t)
	s := newTestServer(t, &config{attestation: a})
	src := s.newAttestationChecker().Bundles.(attest.CachedBundles).Source.(attest.HTTPBundleSource)
	if src.Client.Timeout != 5*time.Second {
		t.Errorf("fetch timeout = %s", src.Client.Timeout)
	}
	if d := newTestServer(t, &config{}).newAttestationChecker().Bundles.(attest.CachedBundles).Source.(attest.HTTPBundleSource); d.Client.Timeout != attest.DefaultBundleFetchTimeout {
		t.Errorf("default fetch timeout = %s", d.Client.Timeout)
	}
	for _, env := range []map[string]string{
		{envFetchTimeout: "0s"}, {envFetchTimeout: "soon"},
		{envRootRefresh: "30s"}, {envRootRefresh: "2m", envRootRefreshTimeout: "5m"},
	} {
		e := newConsumerEnv(t)
		e.env = env
		_, err := e.load()
		wantPolicyRefusal(t, err, "PODIUM_")
	}
}

// Spec: §4.7.11 (User sources of the MCP server) — the remaining policy
// branches: a machine file whose attestation block does not decode refuses
// the start, a user root file that fails the public-good rule refuses it,
// and a repository rule on public-good with no account home refreshes in
// memory.
func TestConsumerAttestation_MoreSourceBranches(t *testing.T) {
	t.Parallel()
	m := newConsumerEnv(t)
	writeFiles(t, m.machineDir, map[string]string{"sync.yaml": "defaults:\n  attestation:\n    polcy: require\n"})
	_, err := m.load()
	wantPolicyRefusal(t, err, "polcy")

	u := newConsumerEnv(t)
	writeFiles(t, u.home, map[string]string{".podium/p.json": publicGoodPolicy, ".podium/gh.json": string(sh.New(t).TrustedRootJSON(sh.GitHubStyleRoot()...))})
	u.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: p.json\n    trusted_root_file: gh.json\n")
	_, err = u.load()
	wantPolicyRefusal(t, err, "gh.json")

	n := newConsumerEnv(t)
	n.home, n.homeErr = "", os.ErrNotExist
	repo := filepath.Join(t.TempDir(), "repo.json")
	writeFiles(t, filepath.Dir(repo), map[string]string{"repo.json": publicGoodPolicy})
	n.env[envAttestationPolicyFile] = repo
	if a := n.mustLoad(t); len(a.refreshers) != 1 {
		t.Errorf("refreshers = %d, want the in-memory user root", len(a.refreshers))
	}
}

// Spec: §4.7.11 (Attestation check, step 3), §6.1.1, §6.2 — a cached
// deprecated record carries its lifecycle members to the check, a result
// that is no map passes through the notices unchanged, the flags of the
// policy set its settings, and an empty scope reads no sync.yaml.
func TestAttestationHelpers(t *testing.T) {
	t.Parallel()
	got := servedForCheck(loadArtifactResponse{Deprecated: true, ReplacedBy: "finance/new"})
	if string(got.Members["deprecated"]) != "true" || string(got.Members["replaced_by"]) != `"finance/new"` {
		t.Errorf("members = %v", got.Members)
	}
	s := newTestServer(t, &config{attestation: &consumerAttestation{notices: []string{"n"}}})
	if out, ok := s.withLoadNotices(resourcesListResult{}, "x").(resourcesListResult); !ok || out.Resources != nil {
		t.Errorf("a non-map result changed: %v", out)
	}
	c := &config{}
	applyConfigKV(c, "attestation-policy", "require")
	applyConfigKV(c, "attestation-policy-file", "p.json")
	if c.attestationPolicy != "require" || c.attestationPolicyFile != "p.json" {
		t.Errorf("config = %+v", c)
	}
	if got := registryFromSyncYAML("", ""); got != "" {
		t.Errorf("registryFromSyncYAML with no scope = %q", got)
	}
	if !ownWorkspaceFile(filepath.Join(t.TempDir(), "absent", "sync.yaml")) {
		t.Error("a workspace file that cannot be examined was read as foreign")
	}
}
