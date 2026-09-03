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
	"os/exec"
	"strings"
	"testing"
)

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
	m := render(t, "runtimeKeys.enabled=true", "runtimeKeys.secretName=rk")

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
	out, err := renderErr(t, "config.objectStore.type=filesystem")
	if err == nil {
		t.Fatal("a filesystem object store rendered with no volume behind it; the registry would disable the store and still report ready")
	}
	if !strings.Contains(out, "objects.enabled") {
		t.Errorf("the refusal does not name the value that fixes it: %s", out)
	}

	m := render(t, "config.objectStore.type=filesystem", "objects.enabled=true")
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
	m := render(t)

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
	m := render(t,
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
	m := render(t)

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
