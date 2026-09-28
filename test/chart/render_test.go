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
	var args []string
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	return renderArgs(t, args...)
}

// renderArgs runs `helm template t <chart>` with the given arguments passed
// through unchanged, such as --set, -f, --namespace, and --is-upgrade.
func renderArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return helmRun(t, append([]string{"template", "t", chartDir}, args...)...)
}

// helmRun runs helm with the given arguments and returns its combined output.
// It skips when helm is absent.
func helmRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	return string(out), err
}

// objectMeta is the metadata subset the render tests read.
type objectMeta struct {
	Name        string            `yaml:"name"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
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

// container is a rendered container. The pod-spec pieces the Deployment and
// the migrate Job share decode as generic values, so a parity assertion
// compares everything the manifest carries rather than a chosen subset.
type container struct {
	Name            string           `yaml:"name"`
	Image           string           `yaml:"image"`
	ImagePullPolicy string           `yaml:"imagePullPolicy"`
	SecurityContext map[string]any   `yaml:"securityContext"`
	Args            []string         `yaml:"args"`
	Ports           []any            `yaml:"ports"`
	Env             []map[string]any `yaml:"env"`
	EnvFrom         []any            `yaml:"envFrom"`
	VolumeMounts    []any            `yaml:"volumeMounts"`
	Resources       map[string]any   `yaml:"resources"`
	StartupProbe    *probe           `yaml:"startupProbe"`
	LivenessProbe   *probe           `yaml:"livenessProbe"`
	ReadinessProbe  *probe           `yaml:"readinessProbe"`
}

// podSpec is a rendered pod spec.
type podSpec struct {
	RestartPolicy                string         `yaml:"restartPolicy"`
	AutomountServiceAccountToken *bool          `yaml:"automountServiceAccountToken"`
	SecurityContext              map[string]any `yaml:"securityContext"`
	Containers                   []container    `yaml:"containers"`
	Volumes                      []any          `yaml:"volumes"`
	NodeSelector                 map[string]any `yaml:"nodeSelector"`
	Tolerations                  []any          `yaml:"tolerations"`
	Affinity                     map[string]any `yaml:"affinity"`
}

// workload is a rendered Deployment or Job, or any other document decoded
// through the same fields.
type workload struct {
	Kind     string     `yaml:"kind"`
	Metadata objectMeta `yaml:"metadata"`
	Spec     struct {
		Replicas *int           `yaml:"replicas"`
		Strategy map[string]any `yaml:"strategy"`
		// Selector is a Deployment's label selector; a Service's selector is
		// a plain map and decodes as Service below.
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"selector"`
		BackoffLimit          *int `yaml:"backoffLimit"`
		ActiveDeadlineSeconds *int `yaml:"activeDeadlineSeconds"`
		Template              struct {
			Metadata objectMeta `yaml:"metadata"`
			Spec     podSpec    `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

// docsOfKind decodes the multi-document output and returns the documents of
// the given kind, each decoded into out's element type through a fresh node.
func docsOfKind(t *testing.T, manifests, kind string) []yaml.Node {
	t.Helper()
	var docs []yaml.Node
	dec := yaml.NewDecoder(strings.NewReader(manifests))
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode manifests: %v", err)
		}
		var head struct {
			Kind string `yaml:"kind"`
		}
		if err := node.Decode(&head); err != nil {
			t.Fatalf("decode kind: %v", err)
		}
		if head.Kind == kind {
			docs = append(docs, node)
		}
	}
	return docs
}

// workloads returns the documents of the given kind decoded as workloads.
func workloads(t *testing.T, manifests, kind string) []workload {
	t.Helper()
	var out []workload
	for _, n := range docsOfKind(t, manifests, kind) {
		var w workload
		if err := n.Decode(&w); err != nil {
			t.Fatalf("decode %s: %v", kind, err)
		}
		out = append(out, w)
	}
	return out
}

// oneWorkload returns the single document of the given kind.
func oneWorkload(t *testing.T, manifests, kind string) workload {
	t.Helper()
	ws := workloads(t, manifests, kind)
	if len(ws) != 1 {
		t.Fatalf("the manifests carry %d %s documents; want 1", len(ws), kind)
	}
	return ws[0]
}

// serviceSelector returns the registry Service's selector.
func serviceSelector(t *testing.T, manifests string) map[string]string {
	t.Helper()
	for _, n := range docsOfKind(t, manifests, "Service") {
		var svc struct {
			Metadata objectMeta `yaml:"metadata"`
			Spec     struct {
				Selector map[string]string `yaml:"selector"`
			} `yaml:"spec"`
		}
		if err := n.Decode(&svc); err != nil {
			t.Fatalf("decode Service: %v", err)
		}
		if svc.Metadata.Labels["app.kubernetes.io/component"] == "" {
			return svc.Spec.Selector
		}
	}
	t.Fatal("the manifests carry no registry Service")
	return nil
}

// containerOf returns the named container of a workload's pod.
func containerOf(t *testing.T, w workload, name string) container {
	t.Helper()
	for _, c := range w.Spec.Template.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the %s carries no %s container", w.Kind, name)
	return container{}
}

// deploymentContainer decodes the manifests and returns the Deployment's
// registry container.
func deploymentContainer(t *testing.T, manifests string) container {
	t.Helper()
	return containerOf(t, oneWorkload(t, manifests, "Deployment"), "podium-server")
}

// envValue returns the value of the named env entry of one decoded container,
// or "" when the entry is absent, so a Deployment assertion never reads a Job
// value and a comment never decides the assertion.
func envValue(c container, name string) string {
	for _, e := range c.Env {
		if e["name"] == name {
			v, _ := e["value"].(string)
			return v
		}
	}
	return ""
}

// findMount returns the container's volume mount of the given name, or nil.
func findMount(c container, name string) map[string]any {
	return findNamed(c.VolumeMounts, name)
}

// findVolume returns the pod volume of the given name, or nil.
func findVolume(w workload, name string) map[string]any {
	return findNamed(w.Spec.Template.Spec.Volumes, name)
}

// findNamed returns the list entry whose name field equals name, or nil.
func findNamed(list []any, name string) map[string]any {
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	return nil
}

// envNames returns the container's env entry names in order.
func envNames(c container) []string {
	names := make([]string, 0, len(c.Env))
	for _, e := range c.Env {
		n, _ := e["name"].(string)
		names = append(names, n)
	}
	return names
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
	c := deploymentContainer(t, render(t, withSigningKey, "runtimeKeys.enabled=true", "runtimeKeys.secretName=rk"))

	path := envValue(c, "PODIUM_RUNTIME_KEYS_PATH")
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

	c := deploymentContainer(t, render(t, withSigningKey, "config.objectStore.type=filesystem", "objects.enabled=true"))
	if root := envValue(c, "PODIUM_FILESYSTEM_ROOT"); root == "" {
		t.Error("objects.enabled renders no PODIUM_FILESYSTEM_ROOT")
	} else if findMount(c, "objects") == nil || findMount(c, "objects")["mountPath"] != root {
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
	c := deploymentContainer(t, render(t, withSigningKey))

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
		if v := envValue(c, key); v != "" {
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
	c := deploymentContainer(t, render(t, withSigningKey))

	for _, key := range []string{
		"PODIUM_BIND",
		"PODIUM_REGISTRY_STORE",
		"PODIUM_OBJECT_STORE",
		"PODIUM_VECTOR_BACKEND",
		"PODIUM_EMBEDDING_PROVIDER",
		"PODIUM_IDENTITY_PROVIDER",
	} {
		if envValue(c, key) == "" {
			t.Errorf("a default install renders no %s", key)
		}
	}
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
	c := deploymentContainer(t, render(t, withSigningKey))

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
	c = deploymentContainer(t, render(t, withSigningKey, "startupProbe.failureThreshold=240"))
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
	d := oneWorkload(t, render(t, withSigningKey), "Deployment")
	c := containerOf(t, d, "podium-server")

	if got := envValue(c, "PODIUM_SIGN"); got != "registry-key" {
		t.Errorf("PODIUM_SIGN is %q; want registry-key", got)
	}
	path := envValue(c, "PODIUM_SIGN_KEY_PATH")
	if !strings.HasPrefix(path, signingMountPath+"/") {
		t.Errorf("PODIUM_SIGN_KEY_PATH is %q, which is not a file inside the mount at %q", path, signingMountPath)
	}
	if m := findMount(c, "signing"); m == nil || m["mountPath"] != signingMountPath || m["readOnly"] != true {
		t.Errorf("no read-only signing mount at %s: %v", signingMountPath, c.VolumeMounts)
	}
	v := findVolume(d, "signing")
	secret, _ := v["secret"].(map[string]any)
	if secret == nil || secret["secretName"] != "sk" {
		t.Errorf("no signing volume from Secret sk: %v", d.Spec.Template.Spec.Volumes)
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
	d := oneWorkload(t, render(t, "signing.mode=none"), "Deployment")
	c := containerOf(t, d, "podium-server")

	if got := envValue(c, "PODIUM_SIGN"); got != "none" {
		t.Errorf("PODIUM_SIGN is %q; want none", got)
	}
	if got := envValue(c, "PODIUM_SIGN_KEY_PATH"); got != "" {
		t.Errorf("signing.mode=none renders PODIUM_SIGN_KEY_PATH=%q", got)
	}
	if findMount(c, "signing") != nil || findVolume(d, "signing") != nil {
		t.Errorf("signing.mode=none renders a signing mount or volume: %v %v", c.VolumeMounts, d.Spec.Template.Spec.Volumes)
	}
}

// A signing-off start over a store that has completed the §13.4 rewrite skips
// it, and one over an unmigrated store with manifest rows exits at start, so
// no signing-off start runs the rewrite under the probes. The startup and
// liveness probes therefore render in both signing modes.
//
// Spec: §13.4
func TestChart_SigningModeNoneKeepsTheProbes(t *testing.T) {
	t.Parallel()
	c := deploymentContainer(t, render(t, "signing.mode=none"))
	if c.StartupProbe == nil || c.LivenessProbe == nil || c.ReadinessProbe == nil {
		t.Errorf("signing.mode=none drops a probe: startup %v, liveness %v, readiness %v", c.StartupProbe, c.LivenessProbe, c.ReadinessProbe)
	}
	if c.StartupProbe != nil && c.StartupProbe.HTTPGet.Path != "/healthz" {
		t.Errorf("signing.mode=none startupProbe path is %q; want /healthz", c.StartupProbe.HTTPGet.Path)
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
