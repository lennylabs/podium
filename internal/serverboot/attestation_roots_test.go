package serverboot

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/internal/testharness/tufharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// envMap is a getenv over a fixed map.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// writeRoot writes raw to a file in dir and returns its path.
func writeRoot(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readTestdataRoot reads one of the trusted roots committed under pkg/sign.
func readTestdataRoot(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "sign", "testdata", "attestation", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Spec: §13.12 — the refresh settings take their defaults when unset or
// blank, accept a Go duration with surrounding white space, and carry a
// config.invalid refusal naming the variable for any other value. The
// maximum age admits 0, which removes the limit.
func TestResolveAttestation_Durations(t *testing.T) {
	t.Parallel()
	now := time.Now()
	def := resolveAttestation(envMap(nil), yamlSigstore{}, "", now)
	if def.refresh.value != 24*time.Hour || def.timeout.value != 30*time.Second || def.maxAge.value != 90*24*time.Hour || len(def.errs) != 0 {
		t.Fatalf("defaults = %v %v %v %v", def.refresh.value, def.timeout.value, def.maxAge.value, def.errs)
	}
	ok := resolveAttestation(envMap(map[string]string{
		sigstoreRootRefreshEnv: " 6h ", sigstoreRootTimeoutEnv: "10s", sigstoreRootMaxAgeEnv: "0",
	}), yamlSigstore{}, "", now)
	if ok.refresh.value != 6*time.Hour || ok.timeout.value != 10*time.Second || ok.maxAge.value != 0 || len(ok.errs) != 0 {
		t.Fatalf("set values = %v %v %v %v", ok.refresh.value, ok.timeout.value, ok.maxAge.value, ok.errs)
	}
	if ok.refresh.source != sigstoreRootRefreshEnv || ok.refresh.display() != "6h0m0s" {
		t.Errorf("refresh source %q display %q", ok.refresh.source, ok.refresh.display())
	}
	for _, tc := range []struct{ env, value, want string }{
		{sigstoreRootRefreshEnv, "0s", "PODIUM_SIGSTORE_ROOT_REFRESH must be a duration above zero"},
		{sigstoreRootRefreshEnv, "24", "PODIUM_SIGSTORE_ROOT_REFRESH must be a duration above zero"},
		{sigstoreRootTimeoutEnv, "-1s", "PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT must be a duration above zero"},
		{sigstoreRootMaxAgeEnv, "-1h", "PODIUM_SIGSTORE_ROOT_MAX_AGE must be a duration of zero or more"},
		{sigstoreRootMaxAgeEnv, "90d", "PODIUM_SIGSTORE_ROOT_MAX_AGE must be a duration of zero or more"},
		{sigstoreRootRefreshEnv, "1ms", "PODIUM_SIGSTORE_ROOT_REFRESH must be at least 1m0s and at least PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT"},
		{sigstoreRootRefreshEnv, "59s", "PODIUM_SIGSTORE_ROOT_REFRESH must be at least 1m0s"},
	} {
		a := resolveAttestation(envMap(map[string]string{tc.env: tc.value}), yamlSigstore{}, "", now)
		err := a.err()
		if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: "+tc.want) {
			t.Errorf("%s=%q: err = %v, want %q", tc.env, tc.value, err, tc.want)
		}
		for _, s := range []durationSetting{a.refresh, a.timeout, a.maxAge} {
			if s.err != nil && s.display() != tc.value+" (invalid)" {
				t.Errorf("%s=%q: display %q", tc.env, tc.value, s.display())
			}
		}
	}
}

// Spec: §13.12 — the refresh interval is at least the refresh deadline, so
// a deadline raised above the default interval refuses the start too, and
// the interval is shown as invalid.
func TestResolveAttestation_RefreshBelowDeadline(t *testing.T) {
	t.Parallel()
	a := resolveAttestation(envMap(map[string]string{sigstoreRootRefreshEnv: "2m", sigstoreRootTimeoutEnv: "3m"}), yamlSigstore{}, "", time.Now())
	if err := a.err(); err == nil || !strings.Contains(err.Error(), "at least PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT (3m0s), got 2m0s") {
		t.Fatalf("err = %v", err)
	}
	if a.refresh.display() != "2m (invalid)" {
		t.Errorf("display = %q", a.refresh.display())
	}
	d := resolveAttestation(envMap(map[string]string{sigstoreRootTimeoutEnv: "25h"}), yamlSigstore{}, "", time.Now())
	if err := d.err(); err == nil || d.refresh.display() != "24h0m0s (invalid)" {
		t.Fatalf("default interval below a 25h deadline: err %v display %q", err, d.refresh.display())
	}
}

// Spec: §4.7.11, §13.12 — the sigstore.instances entry rules, each refused
// with config.invalid naming the entry.
func TestResolveAttestation_InstanceRules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gh := writeRoot(t, dir, "github.json", readTestdataRoot(t, "github.trusted_root.json"))
	for _, tc := range []struct {
		name  string
		entry yamlSigstoreInstance
		want  string
	}{
		{"no name", yamlSigstoreInstance{TrustedRoot: gh}, "has no name"},
		{"public-good", yamlSigstoreInstance{Name: "public-good", TrustedRoot: gh}, "the public-good instance is built in"},
		{"upper case", yamlSigstoreInstance{Name: "Acme", TrustedRoot: gh, TransparencyLog: boolPtr(false)}, "instance name \"Acme\""},
		{"leading dot", yamlSigstoreInstance{Name: ".acme", TrustedRoot: gh, TransparencyLog: boolPtr(false)}, "instance name"},
		{"too long", yamlSigstoreInstance{Name: strings.Repeat("a", 65), TrustedRoot: gh, TransparencyLog: boolPtr(false)}, "instance name"},
		{"no root", yamlSigstoreInstance{Name: "acme", TransparencyLog: boolPtr(false)}, "has no trusted_root"},
		{"github log", yamlSigstoreInstance{Name: "github", TrustedRoot: gh, TransparencyLog: boolPtr(true)}, "the github instance has no transparency log"},
		{"github CT", yamlSigstoreInstance{Name: "github", TrustedRoot: gh, CertificateTransparency: boolPtr(true)}, "the github instance has no transparency log"},
		{"no log statement", yamlSigstoreInstance{Name: "acme", TrustedRoot: gh}, "must state transparency_log"},
		{"unreadable root", yamlSigstoreInstance{Name: "acme", TrustedRoot: filepath.Join(dir, "absent.json"), TransparencyLog: boolPtr(false)}, `Sigstore instance "acme" (sigstore.instances)`},
		{"log the root lacks", yamlSigstoreInstance{Name: "acme", TrustedRoot: gh, TransparencyLog: boolPtr(true)}, "transparency_log is true and the trusted root " + gh + " lists no transparency log"},
		{"CT the root lacks", yamlSigstoreInstance{Name: "acme", TrustedRoot: gh, TransparencyLog: boolPtr(false), CertificateTransparency: boolPtr(true)}, "lists no certificate transparency log"},
	} {
		a := resolveAttestation(envMap(nil), yamlSigstore{Instances: []yamlSigstoreInstance{tc.entry}}, dir, time.Now())
		err := a.err()
		if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want config.invalid with %q", tc.name, err, tc.want)
		}
		if len(a.files) != 0 {
			t.Errorf("%s: %d instances configured, want none", tc.name, len(a.files))
		}
	}
	twice := yamlSigstore{Instances: []yamlSigstoreInstance{{Name: "github", TrustedRoot: gh}, {Name: "github", TrustedRoot: gh}}}
	if err := resolveAttestation(envMap(nil), twice, dir, time.Now()).err(); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("repeated name: err = %v", err)
	}
}

// Spec: §4.7.11, §13.12 — a github entry takes its fixed log requirements,
// an operator-defined entry takes the ones it states, a relative
// trusted_root resolves beside registry.yaml, and each root is read once
// with its read time.
func TestResolveAttestation_Instances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRoot(t, dir, "github.json", readTestdataRoot(t, "github.trusted_root.json"))
	h := sh.New(t)
	writeRoot(t, dir, "acme.json", h.TrustedRootJSON(sh.WithCTLogKey()))
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	a := resolveAttestation(envMap(nil), yamlSigstore{Instances: []yamlSigstoreInstance{
		{Name: "github", TrustedRoot: "github.json", TransparencyLog: boolPtr(false)},
		{Name: "acme-sigstore", TrustedRoot: "acme.json", TransparencyLog: boolPtr(true), CertificateTransparency: boolPtr(true)},
		{Name: "acme-tsa", TrustedRoot: filepath.Join(dir, "acme.json"), TransparencyLog: boolPtr(false)},
	}}, dir, now)
	if err := a.err(); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []struct {
		name     string
		tlog, ct bool
	}{{"github", false, false}, {"acme-sigstore", true, true}, {"acme-tsa", false, false}}
	if len(a.files) != len(want) {
		t.Fatalf("%d instances, want %d", len(a.files), len(want))
	}
	for i, w := range want {
		fi := a.files[i]
		if fi.name != w.name || fi.tlog != w.tlog || fi.ct != w.ct || !filepath.IsAbs(fi.file) || !fi.readAt.Equal(now) || fi.root == nil {
			t.Errorf("instance %d = %+v, want %+v", i, fi, w)
		}
	}
}

// Spec: §4.7.11 — PODIUM_ATTESTATION_TRUSTED_ROOT_FILE that cannot be read,
// that holds several roots, that has a CA-first timestamp authority entry,
// or that lists no certificate transparency log refuses the start with
// config.invalid naming the variable and the file; a valid file becomes
// the public-good root.
func TestResolveAttestation_PublicGoodRootFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := sh.New(t)
	pg := readTestdataRoot(t, "public-good.trusted_root.json")
	var line bytes.Buffer
	if err := json.Compact(&line, pg); err != nil {
		t.Fatal(err)
	}
	lines := append(append(line.Bytes(), '\n'), line.Bytes()...)
	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"unreadable":   {filepath.Join(dir, "absent.json"), "no such file"},
		"two roots":    {writeRoot(t, dir, "two.json", lines), "holds 2 trusted roots"},
		"CA-first TSA": {writeRoot(t, dir, "cafirst.json", h.TrustedRootJSON(sh.WithCTLogKey(), sh.WithCAFirstTSA())), "CA certificate"},
		"no CT log":    {writeRoot(t, dir, "noct.json", h.TrustedRootJSON()), "no certificate transparency log"},
		"not JSON":     {writeRoot(t, dir, "bad.json", []byte("acme")), "invalid"},
	} {
		a := resolveAttestation(envMap(map[string]string{attestationRootFileEnv: tc.path}), yamlSigstore{}, "", time.Now())
		err := a.err()
		if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: ") || !strings.Contains(err.Error(), attestationRootFileEnv) ||
			!strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want config.invalid naming %s, %s, and %q", name, err, attestationRootFileEnv, tc.path, tc.want)
		}
	}
	good := writeRoot(t, dir, "pg.json", pg)
	a := resolveAttestation(envMap(map[string]string{attestationRootFileEnv: " " + good + " "}), yamlSigstore{}, "", time.Now())
	if err := a.err(); err != nil || len(a.files) != 1 || a.files[0].name != sign.PublicGoodInstance || !a.files[0].tlog || !a.files[0].ct {
		t.Fatalf("valid file: err %v files %+v", err, a.files)
	}
}

// Spec: §13.12 — LoadConfig reads sigstore.instances from registry.yaml,
// validate refuses an unreadable root file with config.invalid, `podium
// config show` reports every refusal and every instance, and the §4.7.9
// PODIUM_SIGSTORE_TRUSTED_ROOT_FILE refuses nothing.
// Matrix: §6.10 (config.invalid)
func TestLoadConfig_SigstoreInstances(t *testing.T) {
	dir := t.TempDir()
	writeRoot(t, dir, "github.json", readTestdataRoot(t, "github.trusted_root.json"))
	cfgPath := filepath.Join(dir, "registry.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", dir)
	t.Setenv("PODIUM_CONFIG_FILE", cfgPath)
	t.Setenv("PODIUM_SIGSTORE_TRUSTED_ROOT_FILE", filepath.Join(dir, "absent-keyless-root.json"))
	t.Setenv(sigstoreRootMaxAgeEnv, "-5m")

	write("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: github.json\n      - name: acme\n        trusted_root: missing.json\n        transparency_log: false\n")
	c := LoadConfig()
	err := c.validate()
	if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: ") {
		t.Fatalf("validate = %v, want config.invalid", err)
	}
	errs := c.SettingErrors()
	if len(errs) != 2 || !strings.Contains(errs[0].Error(), sigstoreRootMaxAgeEnv) || !strings.Contains(errs[1].Error(), "missing.json") {
		t.Fatalf("SettingErrors = %v, want the max age and the missing root", errs)
	}
	settings := map[string]Setting{}
	for _, s := range c.Settings() {
		settings[s.Name] = s
	}
	if s := settings[sigstoreInstancesKey]; s.Value != "public-good,github" || s.Source != "registry.yaml" {
		t.Errorf("%s = %+v", sigstoreInstancesKey, s)
	}
	if s := settings["sigstore.instances.github"]; !strings.Contains(s.Value, filepath.Join(dir, "github.json")) || !strings.Contains(s.Value, "transparency_log=false") {
		t.Errorf("github row = %+v", s)
	}
	if s := settings["sigstore.root_max_age"]; s.Value != "-5m (invalid)" || s.Source != sigstoreRootMaxAgeEnv {
		t.Errorf("max age row = %+v", s)
	}
	for _, name := range []string{"attestation.trusted_root_file", "sigstore.root_refresh", "sigstore.root_refresh_timeout"} {
		if _, ok := settings[name]; !ok {
			t.Errorf("Settings has no %s row", name)
		}
	}

	t.Setenv(sigstoreRootMaxAgeEnv, "")
	write("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: github.json\n")
	c = LoadConfig()
	if err := c.validate(); err != nil {
		t.Fatalf("validate with a readable github root and an absent §4.7.9 root file = %v, want nil", err)
	}
	if got := c.SettingErrors(); len(got) != 0 {
		t.Fatalf("SettingErrors = %v, want none", got)
	}
	if c.AttestationInstances() != nil {
		t.Fatal("AttestationInstances before the start is not nil")
	}
}

// Spec: §4.7.11 — the start builds every instance, serves the public-good
// root from the embedded copy until the background refresh completes, then
// from the refreshed copy, which it writes beside the SQLite store. The
// §4.7.9 PODIUM_SIGSTORE_TRUSTED_ROOT_FILE does not become the public-good
// root.
func TestStartAttestationRoots_RefreshesPublicGood(t *testing.T) {
	dir := t.TempDir()
	h := sh.New(t)
	served := h.TrustedRootJSON(sh.WithCTLogKey())
	repo := tufharness.New(t, served)
	ghRoot := readTestdataRoot(t, "github.trusted_root.json")
	writeRoot(t, dir, "github.json", ghRoot)
	cfgPath := writeRoot(t, dir, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: github.json\n"))
	t.Setenv("HOME", dir)
	t.Setenv("PODIUM_CONFIG_FILE", cfgPath)
	t.Setenv("PODIUM_SQLITE_PATH", filepath.Join(dir, "data", "podium.db"))
	t.Setenv("PODIUM_SIGSTORE_TRUSTED_ROOT_FILE", filepath.Join(dir, "github.json"))
	t.Setenv(sigstoreRootMaxAgeEnv, "0")
	c := LoadConfig()
	if err := c.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	c.sigstoreMirror, c.sigstoreTUFRoot = repo.URL(), repo.RootJSON()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.loadAttestationRoots(); err != nil {
		t.Fatalf("loadAttestationRoots: %v", err)
	}
	stopRefresh := c.StartAttestationRefresh(ctx)
	defer stopRefresh()
	inst := c.AttestationInstances()
	pg, ok := inst[sign.PublicGoodInstance]
	if !ok || !pg.TransparencyLog || !pg.CertificateTransparency {
		t.Fatalf("public-good instance = %+v", pg)
	}
	if gh, ok := inst[sign.GitHubInstance]; !ok || gh.TransparencyLog || gh.CertificateTransparency {
		t.Fatalf("github instance = %+v", gh)
	}
	ghDigest := mustDigest(t, ghRoot)
	deadline := time.Now().Add(30 * time.Second)
	for c.attestation.publicGood.Status().Origin != sign.RootRefreshed {
		if time.Now().After(deadline) {
			t.Fatal("the background refresh did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, err := pg.Root.AttestationRoot(ctx)
	if err != nil {
		t.Fatalf("AttestationRoot: %v", err)
	}
	if r.Digest() != mustDigest(t, served) || r.Digest() == ghDigest {
		t.Fatalf("public-good digest = %s, want the refreshed root and never the §4.7.9 root file", r.Digest())
	}
	copyPath := filepath.Join(dir, "data", "sigstore", "public-good", "trusted_root.json")
	if raw, err := os.ReadFile(copyPath); err != nil || mustDigest(t, raw) != r.Digest() {
		t.Fatalf("copy at %s: %v", copyPath, err)
	}
}

// Spec: §4.7.11 — with PODIUM_ATTESTATION_TRUSTED_ROOT_FILE set the start
// makes no refresh, and the public-good root is the file's root with no
// age limit.
func TestStartAttestationRoots_StaticPublicGoodFile(t *testing.T) {
	dir := t.TempDir()
	h := sh.New(t)
	repo := tufharness.New(t, h.TrustedRootJSON(sh.WithCTLogKey()))
	pg := readTestdataRoot(t, "public-good.trusted_root.json")
	t.Setenv("HOME", dir)
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(dir, "absent-registry.yaml"))
	t.Setenv("PODIUM_SQLITE_PATH", filepath.Join(dir, "podium.db"))
	t.Setenv(attestationRootFileEnv, writeRoot(t, dir, "pg.json", pg))
	c := LoadConfig()
	if err := c.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	c.sigstoreMirror, c.sigstoreTUFRoot = repo.URL(), repo.RootJSON()
	if err := c.loadAttestationRoots(); err != nil {
		t.Fatalf("loadAttestationRoots: %v", err)
	}
	c.StartAttestationRefresh(context.Background())()
	if c.attestation.publicGood != nil {
		t.Fatal("a refreshed source was built beside the static root file")
	}
	r, err := c.AttestationInstances()[sign.PublicGoodInstance].Root.AttestationRoot(context.Background())
	if err != nil || r.Digest() != mustDigest(t, pg) {
		t.Fatalf("public-good root = %v, %v, want the file's root", r, err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := repo.Requests(); n != 0 {
		t.Fatalf("the TUF repository received %d requests, want none", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "sigstore")); !os.IsNotExist(err) {
		t.Fatalf("the root directory exists (%v), want none under a static root file", err)
	}
	var row Setting
	for _, s := range c.Settings() {
		if s.Name == sigstoreInstancesKey+"."+sign.PublicGoodInstance {
			row = s
		}
	}
	if row.Source != attestationRootFileEnv || !strings.Contains(row.Value, "transparency_log=true certificate_transparency=true") {
		t.Fatalf("public-good row = %+v, want the file from %s", row, attestationRootFileEnv)
	}
}

// Spec: §4.7.11 — the start logs the digest and the root time of each
// root and a warning for each timestamp authority whose chain holds a
// SHA-1 signature.
func TestLoadAttestationRoots_LogsRootsAndSHA1Authorities(t *testing.T) {
	dir := t.TempDir()
	h := sh.New(t)
	ghRoot := h.TrustedRootJSON(append(sh.GitHubStyleRoot(), sh.WithTimestampAuthorities(sh.TSASHA1))...)
	writeRoot(t, dir, "github.json", ghRoot)
	cfgPath := writeRoot(t, dir, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: github.json\n"))
	t.Setenv("HOME", dir)
	t.Setenv("PODIUM_CONFIG_FILE", cfgPath)
	t.Setenv("PODIUM_SQLITE_PATH", filepath.Join(dir, "podium.db"))
	c := LoadConfig()
	if err := c.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	if err := c.loadAttestationRoots(); err != nil {
		t.Fatalf("loadAttestationRoots: %v", err)
	}
	log.SetOutput(prev)
	if _, err := os.Stat(filepath.Join(dir, "sigstore")); !os.IsNotExist(err) {
		t.Errorf("the start created the root directory (%v), want none before a refresh", err)
	}
	out := buf.String()
	for _, want := range []string{
		"attestation: instance public-good: trusted root " + mustDigest(t, sign.EmbeddedPublicGoodRoot()) + " from embedded (TUF refresh), root time unknown",
		"attestation: instance github: trusted root " + mustDigest(t, ghRoot) + " from file",
		"warning: attestation: instance github: the chain of timestamp authority",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func mustDigest(t *testing.T, raw []byte) string {
	t.Helper()
	r, err := sign.ParseAttestationRoot(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r.Digest()
}
