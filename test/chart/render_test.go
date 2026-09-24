// Render tests drive the chart through `helm template` and assert what the
// manifests actually contain.
//
// The sibling tests in this package read the template files as text, which
// catches a missing line but not a wrong one: a value can reference the right
// key, render without error, and still name a path the registry refuses, or
// carry a default that overrides the secret it was meant to defer to. Every
// defect these tests pin rendered valid YAML and passed the text-level checks.
package chart

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// withSigningKey names the signing key Secret. The chart refuses to render
// without one unless signing.mode is none, so every case whose subject is not
// signing passes it.
const withSigningKey = "signing.secretName=sk"

// render runs `helm template` with the given overrides and returns the
// manifests. It skips when helm is absent so the default `go test ./...` run
// stays clean on a machine without it.
func render(t *testing.T, sets ...string) string {
	t.Helper()
	out, err := renderErr(t, sets...)
	if err != nil {
		t.Fatalf("helm template %v: %v\n%s", sets, err, out)
	}
	return out
}

// renderErr is render without the failure, for the cases that assert a refusal.
func renderErr(t *testing.T, sets ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	args := []string{"template", "t", chartDir}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	return string(out), err
}

// envValue returns the value of the named container env entry, or "" when the
// entry is absent. It reads the line after the name so a comment cannot decide
// the assertion.
func envValue(manifests, name string) string {
	marker := "- name: " + name + "\n"
	i := strings.Index(manifests, marker)
	if i < 0 {
		return ""
	}
	rest := manifests[i+len(marker):]
	line := rest[:strings.Index(rest, "\n")]
	v := strings.TrimSpace(line)
	v = strings.TrimPrefix(v, "value:")
	return strings.Trim(strings.TrimSpace(v), `"`)
}

// The registry reads PODIUM_RUNTIME_KEYS_PATH as a JSON file and treats a read
// failure as fatal, so pointing it at the directory the secret mounts at aborts
// the boot with config.runtime_keys_unavailable under every identity provider.
// The path and the mount come from one value, so this is invisible until a pod
// runs.
//
// Spec: §13.12
func TestChart_RuntimeKeysPathNamesAFileInsideTheMount(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey, "runtimeKeys.enabled=true", "runtimeKeys.secretName=rk")

	path := envValue(m, "PODIUM_RUNTIME_KEYS_PATH")
	if path == "" {
		t.Fatal("runtimeKeys.enabled renders no PODIUM_RUNTIME_KEYS_PATH")
	}
	mount := "/keys"
	if path == mount {
		t.Fatalf("PODIUM_RUNTIME_KEYS_PATH is %q, the directory the secret mounts at; the registry reads it as a file and refuses to start", path)
	}
	if !strings.HasPrefix(path, mount+"/") {
		t.Errorf("PODIUM_RUNTIME_KEYS_PATH is %q, which is not inside the mount at %q", path, mount)
	}
}

// A filesystem object store writes under PODIUM_FILESYSTEM_ROOT. With no volume
// there, the path lands on the read-only container root, the registry logs one
// warning and disables the store, and /readyz still answers 200, so the install
// looks healthy while every resource falls back to inline storage.
//
// Spec: §13.12
func TestChart_FilesystemObjectStoreRequiresItsVolume(t *testing.T) {
	t.Parallel()
	out, err := renderErr(t, withSigningKey, "config.objectStore.type=filesystem")
	if err == nil {
		t.Fatal("a filesystem object store rendered with no volume behind it; the registry would disable the store and still report ready")
	}
	if !strings.Contains(out, "objects.enabled") {
		t.Errorf("the refusal does not name the value that fixes it: %s", out)
	}

	m := render(t, withSigningKey, "config.objectStore.type=filesystem", "objects.enabled=true")
	if root := envValue(m, "PODIUM_FILESYSTEM_ROOT"); root == "" {
		t.Error("objects.enabled renders no PODIUM_FILESYSTEM_ROOT")
	} else if !strings.Contains(m, "mountPath: "+root) {
		t.Errorf("PODIUM_FILESYSTEM_ROOT is %q but no volume mounts there", root)
	}
}

// A container env entry overrides the same key arriving through envFrom, so a
// non-blank default in values.yaml silently replaces what the operator put in
// existingSecret. The chart states this rule for the identity provider and has
// to hold to it for every key the documentation routes through the secret.
//
// Spec: §13.12
func TestChart_DefaultInstallOverridesNoSecretSuppliedKey(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey)

	// Each is named in the clustered-deployment secret recipe, so a default
	// install must leave it to envFrom.
	for _, key := range []string{
		"PODIUM_S3_BUCKET",
		"PODIUM_S3_REGION",
		"PODIUM_S3_ENDPOINT",
		"PODIUM_OAUTH_ISSUER",
		"PODIUM_OAUTH_AUDIENCE",
		"PODIUM_POSTGRES_DSN",
	} {
		if v := envValue(m, key); v != "" {
			t.Errorf("a default install renders %s=%q, which overrides the value existingSecret supplies", key, v)
		}
	}
}

// The bundled database derives its name by suffixing the registry's. Appending
// before truncating drops the suffix once the base reaches the limit, so the
// two workloads collapse onto one name: helm reports the release deployed while
// the Postgres Service overwrites the registry's, leaving the registry
// unreachable on its own Service.
//
// Spec: §13.12
func TestChart_BundledPostgresNameStaysDistinct(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 62)
	m := render(t, withSigningKey,
		"postgresql.enabled=true",
		"postgresql.existingSecret=s",
		"fullnameOverride="+long)

	// Kubernetes scopes a name to its kind, so a Service and a Deployment may
	// share one. Two objects of the same kind may not.
	seen := map[string]int{}
	kind := ""
	for _, line := range strings.Split(m, "\n") {
		if rest, ok := strings.CutPrefix(line, "kind: "); ok {
			kind = strings.TrimSpace(rest)
			continue
		}
		rest, ok := strings.CutPrefix(line, "  name: ")
		if !ok {
			continue
		}
		name := strings.TrimSpace(rest)
		if len(name) > 63 {
			t.Errorf("%s name %q is %d characters, over the 63-character limit", kind, name, len(name))
		}
		seen[kind+"/"+name]++
	}
	for ref, count := range seen {
		if count > 1 {
			t.Errorf("%d objects share %q, so one overwrites the other", count, ref)
		}
	}

	for _, label := range strings.Split(m, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(label), "app.kubernetes.io/name: "); ok {
			if v := strings.TrimSpace(rest); len(v) > 63 {
				t.Errorf("label value %q is %d characters, over the 63-character limit", v, len(v))
			}
		}
	}
}

// The bundled-Postgres entries sit behind a postgresql.enabled guard. A guard
// that closes too late swallows the unconditional entries after it, and an
// external-database install then renders an env block missing its bind address
// and every backend selector, which reads as a working template and fails every
// kubelet probe.
//
// Spec: §13.12
func TestChart_ExternalDatabaseInstallKeepsItsEnvBlock(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey)

	for _, key := range []string{
		"PODIUM_BIND",
		"PODIUM_REGISTRY_STORE",
		"PODIUM_OBJECT_STORE",
		"PODIUM_VECTOR_BACKEND",
		"PODIUM_EMBEDDING_PROVIDER",
		"PODIUM_IDENTITY_PROVIDER",
	} {
		if envValue(m, key) == "" {
			t.Errorf("a default install renders no %s", key)
		}
	}
}

// probe is the subset of a container probe this file reads.
type probe struct {
	HTTPGet struct {
		Path string `yaml:"path"`
		Port string `yaml:"port"`
	} `yaml:"httpGet"`
	PeriodSeconds    int `yaml:"periodSeconds"`
	FailureThreshold int `yaml:"failureThreshold"`
}

// renderedContainer is the registry container as the manifests carry it.
type renderedContainer struct {
	Name           string `yaml:"name"`
	StartupProbe   *probe `yaml:"startupProbe"`
	LivenessProbe  *probe `yaml:"livenessProbe"`
	ReadinessProbe *probe `yaml:"readinessProbe"`
}

// registryContainer decodes the rendered manifests and returns the registry
// container. Decoding rather than matching text is what lets the assertions
// read a probe's own fields instead of a line that happens to sit nearby.
func registryContainer(t *testing.T, manifests string) renderedContainer {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(manifests))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []renderedContainer `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode manifests: %v", err)
		}
		if doc.Kind != "Deployment" {
			continue
		}
		for _, c := range doc.Spec.Template.Spec.Containers {
			if c.Name == "podium-server" {
				return c
			}
		}
	}
	t.Fatal("the manifests carry no podium-server container")
	return renderedContainer{}
}

// The registry answers no probe path until its boot work finishes, and a
// release that migrates a stored value rewrites every stored row on the first
// start of the new version. Without a startup probe the kubelet's liveness
// probe restarts the pod mid-rewrite, and a restart during the planning step
// makes no progress, so the pod restarts forever.
//
// Spec: §13.4
func TestChart_RegistryContainerCarriesAStartupProbe(t *testing.T) {
	t.Parallel()
	c := registryContainer(t, render(t, withSigningKey))

	if c.StartupProbe == nil {
		t.Fatal("the registry container carries no startupProbe, so the kubelet restarts a pod that is still migrating its stored values")
	}
	p := c.StartupProbe
	const minBudget = 600 // ten minutes, in seconds
	if p.HTTPGet.Path != "/healthz" || p.HTTPGet.Port != "http" ||
		p.PeriodSeconds*p.FailureThreshold < minBudget {
		t.Errorf("startupProbe is %s at port %q with a budget of %ds (%d x %d); want /healthz at port \"http\" with at least %ds",
			p.HTTPGet.Path, p.HTTPGet.Port, p.PeriodSeconds*p.FailureThreshold, p.PeriodSeconds, p.FailureThreshold, minBudget)
	}
	if c.LivenessProbe == nil || c.ReadinessProbe == nil {
		t.Error("the startup probe replaced a liveness or readiness probe rather than holding it off")
	}

	// An operator with a large store raises the threshold for the upgrade that
	// migrates stored values, so the value has to reach the manifest.
	c = registryContainer(t, render(t, withSigningKey, "startupProbe.failureThreshold=240"))
	if c.StartupProbe == nil || c.StartupProbe.FailureThreshold != 240 {
		t.Errorf("startupProbe.failureThreshold=240 did not reach the manifest: %+v", c.StartupProbe)
	}
}

// signingMountPath is the chart's default signing.mountPath.
const signingMountPath = "/signing"

// Every replica has to sign under one key, and the pod's root filesystem is
// read-only, so the chart mounts an operator-supplied Secret and points
// PODIUM_SIGN_KEY_PATH at the key file inside it. A path naming the mount
// directory itself, or a mount that is writable or absent, leaves the
// registry unable to load the key the operator supplied.
//
// Spec: §4.7.9, §13.12
func TestChart_SigningKeyMountsTheSecret(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey)

	if got := envValue(m, "PODIUM_SIGN"); got != "registry-key" {
		t.Errorf("PODIUM_SIGN is %q; want registry-key", got)
	}
	path := envValue(m, "PODIUM_SIGN_KEY_PATH")
	if !strings.HasPrefix(path, signingMountPath+"/") {
		t.Errorf("PODIUM_SIGN_KEY_PATH is %q, which is not a file inside the mount at %q", path, signingMountPath)
	}
	mount := "- name: signing\n              mountPath: " + signingMountPath + "\n              readOnly: true\n"
	if !strings.Contains(m, mount) {
		t.Errorf("no read-only signing mount at %s:\n%s", signingMountPath, m)
	}
	volume := "- name: signing\n          secret:\n            secretName: sk\n"
	if !strings.Contains(m, volume) {
		t.Errorf("no signing volume from Secret sk:\n%s", m)
	}
}

// Signing is on by default, and a pod cannot generate a shared key, so a
// render that names no Secret would start replicas that each fail to load a
// key. The chart refuses it at render time and names the value that fixes it.
//
// Spec: §13.10
func TestChart_DefaultRenderRequiresTheSigningSecret(t *testing.T) {
	t.Parallel()
	out, err := renderErr(t)
	if err == nil {
		t.Fatal("a default render succeeded with no signing key Secret")
	}
	if !strings.Contains(out, "signing.secretName") {
		t.Errorf("the refusal does not name signing.secretName: %s", out)
	}
}

// signing.mode=none turns ingest signing off. The render carries the mode and
// nothing that would point the registry at a key file or mount a Secret that
// the operator was not asked to create.
//
// Spec: §13.12
func TestChart_SigningModeNoneMountsNoKey(t *testing.T) {
	t.Parallel()
	m := render(t, "signing.mode=none")

	if got := envValue(m, "PODIUM_SIGN"); got != "none" {
		t.Errorf("PODIUM_SIGN is %q; want none", got)
	}
	if got := envValue(m, "PODIUM_SIGN_KEY_PATH"); got != "" {
		t.Errorf("signing.mode=none renders PODIUM_SIGN_KEY_PATH=%q", got)
	}
	if strings.Contains(m, "- name: signing\n") {
		t.Errorf("signing.mode=none renders a signing mount or volume:\n%s", m)
	}
}

// An unknown mode would otherwise reach the registry as PODIUM_SIGN and fail
// the boot on every replica. The chart refuses it at render time and names
// the accepted values.
//
// Spec: §13.12
func TestChart_UnknownSigningModeFailsTheRender(t *testing.T) {
	t.Parallel()
	out, err := renderErr(t, withSigningKey, "signing.mode=sigstore-keyless")
	if err == nil {
		t.Fatal("an unknown signing.mode rendered")
	}
	for _, want := range []string{"signing.mode", "registry-key", "none"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q: %s", want, out)
		}
	}
}
